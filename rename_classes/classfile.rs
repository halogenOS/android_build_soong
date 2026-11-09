// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

//! Renames classes inside one class file with the results that jarjar produces through ASM. Each
//! place that reads a UTF8 entry of the constant pool is mapped by how it reads the entry. An
//! entry whose places all get the same new text is rewritten where it is, so the pool has the
//! same layout as before. Otherwise each place whose text changes reads an entry with the new
//! text, taken from the pool when it is there and added at the end of the pool otherwise.
//!
//! Names of members, attributes, local variables, parameters and annotation elements are read
//! as plain text and never mapped. Places not parsed here, such as the attributes of a module,
//! read identifiers, which cannot contain the `.` or `/` of a renamed class name.
//!
//! Methods have `max_stack` and `max_locals` as the compiler wrote them, where ASM computes them
//! again for jarjar. Both values are valid for the same code.

mod parse;

use crate::remap::{MalformedSignature, Remapper};
use parse::{Annotation, AnnotationTable, ClassFile, Constant, ConstantTag, Site, Usage};
use std::collections::{hash_map, BTreeMap, HashMap};
use std::fmt;
use std::num::TryFromIntError;

/// The annotation that jarjar removes from repackaged classes with
/// -DremoveAndroidCompatAnnotations=true.
const COMPAT_ANNOTATION: &str = "Landroid/compat/annotation/UnsupportedAppUsage;";

#[derive(Debug)]
pub enum ClassError {
    Truncated,
    NotAClass,
    BadConstant {
        index: u16,
    },
    BadTag(u8),
    BadElementValue(u8),
    BadTargetType(u8),
    PoolFull(TryFromIntError),
    /// A renamed text does not fit into the u2 length of its UTF8 entry.
    EntryTooLong {
        index: u16,
        source: TryFromIntError,
    },
    /// An annotation attribute without the compat annotation does not fit into its length
    /// fields.
    AttributeTooLong {
        offset: usize,
        source: TryFromIntError,
    },
    Signature {
        index: u16,
        source: MalformedSignature,
    },
}

impl fmt::Display for ClassError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Truncated => write!(f, "the class file ends early"),
            Self::NotAClass => write!(f, "not a class file"),
            Self::BadConstant { index } => write!(f, "constant {index} has the wrong kind"),
            Self::BadTag(tag) => write!(f, "unknown constant pool tag {tag}"),
            Self::BadElementValue(tag) => write!(f, "unknown annotation element tag {tag}"),
            Self::BadTargetType(tag) => write!(f, "unknown type annotation target {tag:#x}"),
            Self::PoolFull(_) => {
                write!(f, "the renamed constant pool has more than 65535 entries")
            }
            Self::EntryTooLong { index, .. } => {
                write!(f, "the renamed text of constant {index} is longer than 65535 bytes")
            }
            Self::AttributeTooLong { offset, .. } => write!(
                f,
                "the annotation attribute at byte {offset} does not fit into its length fields"
            ),
            Self::Signature { index, .. } => {
                write!(f, "cannot rename the signature in constant {index}")
            }
        }
    }
}

impl std::error::Error for ClassError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::PoolFull(source)
            | Self::EntryTooLong { source, .. }
            | Self::AttributeTooLong { source, .. } => Some(source),
            Self::Signature { source, .. } => Some(source),
            Self::Truncated
            | Self::NotAClass
            | Self::BadConstant { .. }
            | Self::BadTag(_)
            | Self::BadElementValue(_)
            | Self::BadTargetType(_) => None,
        }
    }
}

/// The result for one class file.
pub struct Renamed {
    /// The internal name of the class after renaming.
    pub class_name: String,
    /// The new class file, when anything in it changed.
    pub bytes: Option<Vec<u8>>,
}

/// A replacement of the bytes from `start` to `end` of the class file.
struct Splice {
    start: usize,
    end: usize,
    bytes: Vec<u8>,
}

/// The new text of each place, grouped by the entry it reads, in the order of the pool. Each
/// place is the offset of its u2 field and its new text, or None when the text does not change.
type NewTexts = BTreeMap<u16, Vec<(usize, Option<String>)>>;

/// The changes to the constant pool.
struct PoolEdit {
    splices: Vec<Splice>,
    pool_count: u16,
}

/// An annotation attribute that loses the compat annotation.
struct AnnotationRemoval<'a> {
    table: &'a AnnotationTable,
    kept: Vec<&'a Annotation>,
}

/// Encodes the UTF8 constant pool entry for `text`.
fn encode_utf8_entry(text: &str) -> Result<Vec<u8>, TryFromIntError> {
    let bytes = cesu8::to_java_cesu8(text);
    let length = u16::try_from(bytes.len())?;
    let mut entry = Vec::with_capacity(bytes.len() + 3);
    entry.push(ConstantTag::Utf8 as u8);
    entry.extend_from_slice(&length.to_be_bytes());
    entry.extend_from_slice(&bytes);
    Ok(entry)
}

/// Finds the new text given to every place that reads an entry, when it is the same for all of
/// them.
fn find_shared_new_text(uses: &[(usize, Option<String>)]) -> Option<&str> {
    let (_, first) = uses.first()?;
    let text = first.as_deref()?;
    uses.iter().all(|(_, other)| other.as_deref() == Some(text)).then_some(text)
}

fn read_u16(data: &[u8], offset: usize) -> u16 {
    u16::from_be_bytes([data[offset], data[offset + 1]])
}

/// Maps the text of the entry read by `site`, or returns None when the text does not change.
fn map_site(
    class: &ClassFile,
    site: &Site,
    remapper: &Remapper,
) -> Result<Option<String>, ClassError> {
    if class.is_undecodable_utf8(site.utf8) {
        return Ok(None);
    }
    let text = || class.utf8_text(site.utf8);
    Ok(match site.usage {
        Usage::Plain => None,
        Usage::ClassName => remapper.map_type(text()?),
        Usage::Descriptor => remapper.map_desc(text()?),
        Usage::Signature => remapper
            .map_signature(text()?)
            .map_err(|source| ClassError::Signature { index: site.utf8, source })?,
        Usage::Value => remapper.map_value(text()?),
        Usage::InnerName { inner_class } => {
            remapper.map_inner_class_name(class.class_name(inner_class)?, text()?)
        }
    })
}

/// Maps the text of every place.
fn map_sites(class: &ClassFile, remapper: &Remapper) -> Result<NewTexts, ClassError> {
    let mut by_entry = NewTexts::new();
    for site in &class.sites {
        by_entry
            .entry(site.utf8)
            .or_default()
            .push((site.offset, map_site(class, site, remapper)?));
    }
    Ok(by_entry)
}

/// Rewrites the entries whose places all get the same new text, and adds an entry for each other
/// new text that the pool does not have. Points each place whose text changes at its entry in
/// `class_bytes`.
fn edit_pool(
    class: &ClassFile,
    new_texts: &NewTexts,
    class_bytes: &mut [u8],
) -> Result<PoolEdit, ClassError> {
    // The entry with each text. Of entries with equal text, the first in the pool is used, so
    // the map is built in reverse order of the pool.
    let mut entries: HashMap<&str, u16> =
        class.texts.iter().rev().map(|(&index, text)| (text.as_str(), index)).collect();
    let mut splices = Vec::new();
    let mut per_site = Vec::new();
    for (&utf8, uses) in new_texts {
        let Some(text) = find_shared_new_text(uses) else {
            per_site.push(uses);
            continue;
        };
        let Some(Constant::Utf8 { start, end }) = class.pool.get(usize::from(utf8)).copied() else {
            return Err(ClassError::BadConstant { index: utf8 });
        };
        let bytes = encode_utf8_entry(text)
            .map_err(|source| ClassError::EntryTooLong { index: utf8, source })?;
        splices.push(Splice { start, end, bytes });
        // Of the texts in the map, only the old text of this entry can lead to it, and only when
        // no earlier entry has that text.
        if let Some(old) = class.texts.get(&utf8) {
            if entries.get(old.as_str()) == Some(&utf8) {
                let _index_of_old_text = entries.remove(old.as_str());
            }
        }
        *entries.entry(text).or_default() = utf8;
    }

    let mut appended = Vec::new();
    let mut pool_count = class.pool.len();
    for (offset, text) in per_site.into_iter().flatten() {
        let Some(text) = text else { continue };
        let index = match entries.entry(text) {
            hash_map::Entry::Occupied(entry) => *entry.get(),
            hash_map::Entry::Vacant(slot) => {
                let index = u16::try_from(pool_count).map_err(ClassError::PoolFull)?;
                appended.extend(
                    encode_utf8_entry(text)
                        .map_err(|source| ClassError::EntryTooLong { index, source })?,
                );
                pool_count += 1;
                *slot.insert(index)
            }
        };
        class_bytes[*offset..*offset + 2].copy_from_slice(&index.to_be_bytes());
    }
    splices.push(Splice { start: class.pool_end, end: class.pool_end, bytes: appended });
    let pool_count = u16::try_from(pool_count).map_err(ClassError::PoolFull)?;
    Ok(PoolEdit { splices, pool_count })
}

/// Finds the annotation attributes that contain the compat annotation.
fn find_compat_annotations(class: &ClassFile) -> Result<Vec<AnnotationRemoval<'_>>, ClassError> {
    let mut removals = Vec::new();
    for table in &class.annotation_tables {
        let mut kept = Vec::new();
        for annotation in &table.annotations {
            if class.utf8_text(annotation.type_index)? != COMPAT_ANNOTATION {
                kept.push(annotation);
            }
        }
        if kept.len() < table.annotations.len() {
            removals.push(AnnotationRemoval { table, kept });
        }
    }
    Ok(removals)
}

/// Removes the compat annotation from each attribute of `removals`. Decrements the
/// `attributes_count` in `class_bytes` where no annotation is kept, and returns the splices that
/// replace each attribute. The kept annotations are read from `class_bytes`, so this runs after
/// `edit_pool` has pointed their places at the new entries.
fn remove_annotations(
    removals: &[AnnotationRemoval],
    class_bytes: &mut [u8],
) -> Result<Vec<Splice>, ClassError> {
    let mut splices = Vec::new();
    for AnnotationRemoval { table, kept } in removals {
        let bytes = if kept.is_empty() {
            // The table contains the removed attribute, so its count is at least 1.
            let count = read_u16(class_bytes, table.count_offset) - 1;
            class_bytes[table.count_offset..table.count_offset + 2]
                .copy_from_slice(&count.to_be_bytes());
            Vec::new()
        } else {
            let body: Vec<u8> = kept
                .iter()
                .flat_map(|annotation| {
                    class_bytes[annotation.start..annotation.end].iter().copied()
                })
                .collect();
            let too_long = |source| ClassError::AttributeTooLong { offset: table.start, source };
            let length = u32::try_from(body.len() + 2).map_err(too_long)?;
            let count = u16::try_from(kept.len()).map_err(too_long)?;
            let mut attribute = class_bytes[table.start..table.start + 2].to_vec();
            attribute.extend_from_slice(&length.to_be_bytes());
            attribute.extend_from_slice(&count.to_be_bytes());
            attribute.extend(body);
            attribute
        };
        splices.push(Splice { start: table.start, end: table.end, bytes });
    }
    Ok(splices)
}

/// Returns `class_bytes` with each splice in place of the bytes it replaces. Splices do not
/// overlap.
fn apply_splices(class_bytes: &[u8], mut splices: Vec<Splice>) -> Vec<u8> {
    splices.sort_by_key(|splice| (splice.start, splice.end));
    let mut out = Vec::with_capacity(
        class_bytes.len() + splices.iter().map(|s| s.bytes.len()).sum::<usize>(),
    );
    let mut position = 0;
    for splice in splices {
        out.extend_from_slice(&class_bytes[position..splice.start]);
        out.extend(splice.bytes);
        position = splice.end;
    }
    out.extend_from_slice(&class_bytes[position..]);
    out
}

/// Renames the classes in one class file. With `remove_compat_annotations`, removes the compat
/// annotation from the class, its fields and its methods when the class is renamed, as jarjar
/// does with -DremoveAndroidCompatAnnotations=true.
pub fn rename(
    data: &[u8],
    remapper: &Remapper,
    remove_compat_annotations: bool,
) -> Result<Renamed, ClassError> {
    let class = ClassFile::parse(data)?;
    let this_name = class.class_name(class.this_class)?;
    let new_name = remapper.map_internal(this_name);
    let class_name = new_name.unwrap_or(this_name).to_owned();

    let new_texts = map_sites(&class, remapper)?;
    let removals = if remove_compat_annotations && new_name.is_some() {
        find_compat_annotations(&class)?
    } else {
        Vec::new()
    };
    if removals.is_empty() && new_texts.values().flatten().all(|(_, text)| text.is_none()) {
        return Ok(Renamed { class_name, bytes: None });
    }

    let mut patched = data.to_vec();
    let PoolEdit { mut splices, pool_count } = edit_pool(&class, &new_texts, &mut patched)?;
    splices.push(Splice {
        start: class.pool_count_offset,
        end: class.pool_count_offset + 2,
        bytes: pool_count.to_be_bytes().to_vec(),
    });
    splices.extend(remove_annotations(&removals, &mut patched)?);
    Ok(Renamed { class_name, bytes: Some(apply_splices(&patched, splices)) })
}
