// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

//! Writes the jar that `jarjar process` writes for exact renames: the same entries, names and
//! modification times, in the same order.

use crate::classfile::{self, ClassError};
use crate::remap::Remapper;
use std::collections::{btree_map, BTreeMap};
use std::fmt;
use std::fs::{self, File, Permissions};
use std::io::{self, BufWriter, Cursor, Read, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use zip::read::ZipFile;
use zip::result::ZipError;
use zip::write::FileOptions;
use zip::{CompressionMethod, DateTime, ZipArchive, ZipWriter};

const SERVICES_PREFIX: &str = "META-INF/services/";

/// The input jar, read into memory with one sequential read. Renaming visits each entry up to
/// three times, at offsets spread over the whole file.
type InputJar = ZipArchive<Cursor<Vec<u8>>>;

#[derive(Debug)]
pub enum JarError {
    ReadInput(io::Error),
    ParseInput(ZipError),
    ReadEntry {
        name: String,
        source: io::Error,
    },
    RenameClass {
        name: String,
        source: ClassError,
    },
    /// Two different files get the same name in the output.
    DuplicateEntry {
        name: String,
    },
    CreateOutput(io::Error),
    WriteOutput(ZipError),
    /// The complete output jar cannot be moved from its temporary file to the output path.
    MoveOutput(io::Error),
}

impl fmt::Display for JarError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::ReadInput(_) => write!(f, "cannot read the input jar"),
            Self::ParseInput(_) => write!(f, "cannot parse the input jar"),
            Self::ReadEntry { name, .. } => write!(f, "cannot read {name} in the input jar"),
            Self::RenameClass { name, .. } => write!(f, "cannot rename the classes in {name}"),
            Self::DuplicateEntry { name } => write!(f, "Duplicate jar entries: {name}"),
            Self::CreateOutput(_) => write!(f, "cannot create the output jar"),
            Self::WriteOutput(_) => write!(f, "cannot write the output jar"),
            Self::MoveOutput(_) => write!(f, "cannot move the output jar into place"),
        }
    }
}

impl std::error::Error for JarError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::ReadInput(error) | Self::CreateOutput(error) | Self::MoveOutput(error) => {
                Some(error)
            }
            Self::ReadEntry { source, .. } => Some(source),
            Self::ParseInput(error) | Self::WriteOutput(error) => Some(error),
            Self::RenameClass { source, .. } => Some(source),
            Self::DuplicateEntry { .. } => None,
        }
    }
}

/// An entry of the output jar.
enum OutputEntry {
    /// Copied with its compressed data from this entry of the input jar.
    Copied(usize),
    Written {
        data: Vec<u8>,
        time: DateTime,
    },
}

/// The end of the name of a class file, which jarjar compares case-sensitively.
const CLASS_SUFFIX: &str = ".class";

/// Whether jarjar treats the entry as a class file.
fn is_class(name: &str) -> bool {
    name.ends_with(CLASS_SUFFIX) && !name.starts_with("META-INF/version")
}

fn read_entry(file: &mut ZipFile<'_>, name: &str) -> Result<Vec<u8>, JarError> {
    let mut data = Vec::new();
    let _length = file
        .read_to_end(&mut data)
        .map_err(|source| JarError::ReadEntry { name: name.to_owned(), source })?;
    Ok(data)
}

/// Converts a time to the date and the time fields of the MS-DOS form. Every time comes from
/// those fields of an input entry, so this form compares them exactly.
fn to_msdos_fields(time: DateTime) -> (u16, u16) {
    (time.datepart(), time.timepart())
}

/// Reads the data and the modification time of an entry, which jarjar compares for duplicates.
fn read_entry_content(
    entry: &OutputEntry,
    input: &mut InputJar,
) -> Result<(Vec<u8>, (u16, u16)), JarError> {
    match entry {
        OutputEntry::Copied(index) => {
            let mut file = input.by_index(*index).map_err(JarError::ParseInput)?;
            let name = file.name().to_owned();
            let time = to_msdos_fields(file.last_modified());
            Ok((read_entry(&mut file, &name)?, time))
        }
        OutputEntry::Written { data, time } => Ok((data.clone(), to_msdos_fields(*time))),
    }
}

/// Maps the name and each line of a service file, as the `ServiceProcessor` of jarjar does.
fn map_service(name: &str, data: &[u8], remapper: &Remapper) -> (String, Vec<u8>) {
    let service = &name[SERVICES_PREFIX.len()..];
    let name =
        format!("{SERVICES_PREFIX}{}", remapper.map_value(service).as_deref().unwrap_or(service));
    let text = String::from_utf8_lossy(data);
    let mut out = String::with_capacity(text.len());
    for line in text.lines() {
        out.push_str(remapper.map_value(line).as_deref().unwrap_or(line));
        out.push('\n');
    }
    if out.is_empty() {
        out.push('\n');
    }
    (name, out.into_bytes())
}

/// Returns the name and the content of the output entry made from the input entry at `index`.
fn rename_entry(
    input: &mut InputJar,
    index: usize,
    remapper: &Remapper,
    remove_compat_annotations: bool,
) -> Result<(String, OutputEntry), JarError> {
    let mut file = input.by_index(index).map_err(JarError::ParseInput)?;
    let name = file.name().to_owned();
    let time = file.last_modified();
    if is_class(&name) && !name.starts_with("META-INF") {
        let data = read_entry(&mut file, &name)?;
        let renamed = classfile::rename(&data, remapper, remove_compat_annotations)
            .map_err(|source| JarError::RenameClass { name, source })?;
        let entry = renamed
            .bytes
            .map_or(OutputEntry::Copied(index), |data| OutputEntry::Written { data, time });
        Ok((format!("{}{CLASS_SUFFIX}", renamed.class_name), entry))
    } else if is_class(&name) {
        Ok((name, OutputEntry::Copied(index)))
    } else if name.starts_with(SERVICES_PREFIX) && !name.ends_with('/') {
        let data = read_entry(&mut file, &name)?;
        let (name, data) = map_service(&name, &data, remapper);
        Ok((name, OutputEntry::Written { data, time }))
    } else {
        Ok((remapper.map_path(&name).unwrap_or(name), OutputEntry::Copied(index)))
    }
}

/// Adds an entry the way jarjar does: a second directory of the same name is dropped, a second
/// file of the same name must be the same file.
fn add_entry(
    entries: &mut BTreeMap<String, OutputEntry>,
    name: String,
    entry: OutputEntry,
    input: &mut InputJar,
) -> Result<(), JarError> {
    match entries.entry(name) {
        btree_map::Entry::Vacant(slot) => {
            let _new_entry = slot.insert(entry);
            Ok(())
        }
        btree_map::Entry::Occupied(existing) => {
            if existing.key().ends_with('/')
                || read_entry_content(existing.get(), input)? == read_entry_content(&entry, input)?
            {
                Ok(())
            } else {
                Err(JarError::DuplicateEntry { name: existing.key().clone() })
            }
        }
    }
}

/// Writes the entries in reverse order of their names and drops each directory that does not
/// contain a later entry, as jarjar does.
fn write_entries(
    entries: &BTreeMap<String, OutputEntry>,
    input: &mut InputJar,
    file: &File,
) -> Result<(), JarError> {
    let mut writer = ZipWriter::new(BufWriter::new(file));
    let mut last_written: Option<&str> = None;
    for (name, entry) in entries.iter().rev() {
        if name.ends_with('/') && !last_written.is_some_and(|next| next.starts_with(name.as_str()))
        {
            continue;
        }
        match entry {
            OutputEntry::Copied(index) => {
                let file = input.by_index_raw(*index).map_err(JarError::ParseInput)?;
                writer.raw_copy_file_rename(file, name).map_err(JarError::WriteOutput)?;
            }
            OutputEntry::Written { data, time } => {
                let options = FileOptions::default()
                    .compression_method(CompressionMethod::Deflated)
                    .last_modified_time(*time);
                writer.start_file(name, options).map_err(JarError::WriteOutput)?;
                writer.write_all(data).map_err(|error| JarError::WriteOutput(error.into()))?;
            }
        }
        last_written = Some(name);
    }
    writer
        .finish()
        .map_err(JarError::WriteOutput)?
        .flush()
        .map_err(|error| JarError::WriteOutput(error.into()))
}

/// Writes the jar to a temporary file in the directory of `output` and moves it to `output` once
/// it is complete, so a failure leaves no truncated jar at `output`. The temporary file gets the
/// mode that `File::create` gives a new file.
fn write_jar(
    entries: &BTreeMap<String, OutputEntry>,
    input: &mut InputJar,
    output: &Path,
) -> Result<(), JarError> {
    let directory = output.parent().unwrap_or(Path::new("."));
    let temporary = tempfile::Builder::new()
        .permissions(Permissions::from_mode(0o666))
        .tempfile_in(directory)
        .map_err(JarError::CreateOutput)?;
    write_entries(entries, input, temporary.as_file())?;
    temporary.into_temp_path().persist(output).map_err(|error| JarError::MoveOutput(error.error))
}

/// Renames the classes of the input jar and writes the result to `output`. With
/// `remove_compat_annotations`, removes the compat annotation from renamed classes.
pub fn rename(
    input: &Path,
    output: &Path,
    remapper: &Remapper,
    remove_compat_annotations: bool,
) -> Result<(), JarError> {
    let data = fs::read(input).map_err(JarError::ReadInput)?;
    let mut input = ZipArchive::new(Cursor::new(data)).map_err(JarError::ParseInput)?;
    let mut entries = BTreeMap::new();
    for index in 0..input.len() {
        let (name, entry) = rename_entry(&mut input, index, remapper, remove_compat_annotations)?;
        add_entry(&mut entries, name, entry, &mut input)?;
    }
    write_jar(&entries, &mut input, output)
}
