// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

//! The name mapping of jarjar (its `PackageRemapper` on top of the `Remapper` of ASM) for exact
//! renames. Every function returns the new text, or None when the text does not change.

use crate::rules::Renames;
use std::fmt;

/// A signature that does not follow the grammar of the class file format.
#[derive(Debug)]
pub struct MalformedSignature {
    signature: String,
    /// The byte position where the grammar does not allow what is there.
    position: usize,
    /// The byte at `position`, or None at the end of the signature.
    found: Option<u8>,
}

impl fmt::Display for MalformedSignature {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let Self { signature, position, found } = self;
        match found {
            Some(byte) => write!(
                f,
                "malformed signature {signature:?}: unexpected {:?} at byte {position}",
                char::from(*byte)
            ),
            None => write!(f, "malformed signature {signature:?}: ends early at byte {position}"),
        }
    }
}

impl std::error::Error for MalformedSignature {}

/// jarjar puts this word in place of the file name to match the directory of a resource.
const RESOURCE_SUFFIX: &str = "RESOURCE";

pub struct Remapper {
    renames: Renames,
}

impl Remapper {
    pub fn new(renames: Renames) -> Self {
        Self { renames }
    }

    /// The internal name of a class, such as `a/b/C`.
    pub fn map_internal(&self, name: &str) -> Option<&str> {
        self.renames.get(name)
    }

    /// The name in a `CONSTANT_Class` entry: an internal name or an array descriptor.
    pub fn map_type(&self, name: &str) -> Option<String> {
        if name.starts_with('[') {
            self.map_desc(name)
        } else {
            self.map_internal(name).map(str::to_owned)
        }
    }

    /// A field or method descriptor. In a descriptor every `L` starts a class name ended by `;`.
    pub fn map_desc(&self, desc: &str) -> Option<String> {
        if !desc.contains('L') {
            return None;
        }
        let mut out = String::with_capacity(desc.len());
        let mut changed = false;
        let mut rest = desc;
        while let Some(start) = rest.find('L') {
            out.push_str(&rest[..=start]);
            let tail = &rest[start + 1..];
            let Some(end) = tail.find(';') else {
                out.push_str(tail);
                rest = "";
                break;
            };
            let name = &tail[..end];
            match self.map_internal(name) {
                Some(mapped) => {
                    out.push_str(mapped);
                    changed = true;
                }
                None => out.push_str(name),
            }
            rest = &tail[end..];
        }
        out.push_str(rest);
        changed.then_some(out)
    }

    /// A class, field or method signature with generic types.
    pub fn map_signature(&self, signature: &str) -> Result<Option<String>, MalformedSignature> {
        if !signature.contains('L') {
            return Ok(None);
        }
        let mut walker = SignatureWalker {
            remapper: self,
            text: signature,
            pos: 0,
            out: String::with_capacity(signature.len()),
            changed: false,
        };
        walker.walk()?;
        Ok(walker.changed.then_some(walker.out))
    }

    /// A string constant, as PackageRemapper.mapValue maps it.
    pub fn map_value(&self, value: &str) -> Option<String> {
        if is_array_for_name(value) {
            return self.map_desc(&value.replace('.', "/")).map(|desc| desc.replace('/', "."));
        }
        if let Some(path) = self.map_path(value) {
            return Some(path);
        }
        match (value.contains('.'), value.contains('/')) {
            (true, true) => None,
            (true, false) => {
                self.map_internal(&value.replace('.', "/")).map(|name| name.replace('/', "."))
            }
            (false, _) => self.map_internal(value).map(str::to_owned),
        }
    }

    /// The path of a resource: its directory is matched as the class `<directory>/RESOURCE`.
    pub fn map_path(&self, path: &str) -> Option<String> {
        let (directory, file) = match path.rfind('/') {
            Some(slash) => (&path[..=slash], &path[slash + 1..]),
            None => ("", path),
        };
        let absolute = directory.starts_with('/');
        let directory = directory.strip_prefix('/').unwrap_or(directory);
        let mapped = self.map_internal(&format!("{directory}{RESOURCE_SUFFIX}"))?;
        let mapped_directory = mapped.strip_suffix(RESOURCE_SUFFIX)?;
        Some(format!("{}{mapped_directory}{file}", if absolute { "/" } else { "" }))
    }

    /// The simple name of an inner class in the `InnerClasses` attribute, as ASM maps it.
    pub fn map_inner_class_name(&self, name: &str, inner_name: &str) -> Option<String> {
        let remapped = self.map_type(name)?;
        if let (Some(origin), Some(split)) = (name.rfind('/'), remapped.rfind('/')) {
            if name[origin..] == remapped[split..] {
                return None;
            }
        }
        let dollar = remapped.rfind('$')?;
        let simple = remapped[dollar + 1..].trim_start_matches(|c: char| c.is_ascii_digit());
        (simple != inner_name).then(|| simple.to_owned())
    }
}

/// Whether `value` is the name of an array class as Class.forName reads it, such as
/// `[La.b.C;`, which `PackageRemapper` matches with `\[L[\p{javaJavaIdentifierPart}\.]+?;`.
fn is_array_for_name(value: &str) -> bool {
    value.strip_prefix("[L").and_then(|rest| rest.strip_suffix(';')).is_some_and(|name| {
        !name.is_empty() && name.chars().all(|c| c == '.' || is_java_identifier_part(c))
    })
}

/// Character.isJavaIdentifierPart for the characters of class names: letters, digits, `$`, `_`
/// and the characters ignorable in identifiers. Outside ASCII it follows the Unicode classes of
/// Rust, which differ from those of Java for a few currency symbols, connecting punctuation and
/// combining marks. A string that contains one of them is mapped only when a rule matches that
/// class.
fn is_java_identifier_part(c: char) -> bool {
    c.is_alphanumeric()
        || matches!(c, '$' | '_' | '\u{0}'..='\u{8}' | '\u{e}'..='\u{1b}' | '\u{7f}'..='\u{9f}')
}

/// Walks a signature by the grammar of JVMS 4.7.9.1 and writes it with the classes renamed,
/// as the `SignatureRemapper` of ASM does.
struct SignatureWalker<'a> {
    remapper: &'a Remapper,
    text: &'a str,
    pos: usize,
    out: String,
    changed: bool,
}

type Step = Result<(), MalformedSignature>;

impl<'a> SignatureWalker<'a> {
    fn peek(&self) -> Option<u8> {
        self.text.as_bytes().get(self.pos).copied()
    }

    /// The error for what the grammar does not allow at `position`.
    fn build_malformed_at(&self, position: usize) -> MalformedSignature {
        MalformedSignature {
            signature: self.text.to_owned(),
            position,
            found: self.text.as_bytes().get(position).copied(),
        }
    }

    fn build_malformed(&self) -> MalformedSignature {
        self.build_malformed_at(self.pos)
    }

    fn copy_byte(&mut self) -> Step {
        let byte = self.peek().ok_or_else(|| self.build_malformed())?;
        self.out.push(char::from(byte));
        self.pos += 1;
        Ok(())
    }

    /// Returns the text before the first of `ends`.
    fn take_until(&mut self, ends: &[u8]) -> Result<&'a str, MalformedSignature> {
        let text = self.text;
        let start = self.pos;
        let length = text.as_bytes()[start..]
            .iter()
            .position(|b| ends.contains(b))
            .ok_or_else(|| self.build_malformed_at(text.len()))?;
        self.pos += length;
        Ok(&text[start..start + length])
    }

    fn walk(&mut self) -> Step {
        if self.peek() == Some(b'<') {
            self.walk_type_parameters()?;
        }
        while let Some(byte) = self.peek() {
            match byte {
                b'(' | b')' | b'^' => self.copy_byte()?,
                _ => self.walk_type_signature()?,
            }
        }
        Ok(())
    }

    fn walk_type_parameters(&mut self) -> Step {
        self.copy_byte()?;
        while self.peek() != Some(b'>') {
            let identifier = self.take_until(b":")?;
            self.out.push_str(identifier);
            while self.peek() == Some(b':') {
                self.copy_byte()?;
                if matches!(self.peek(), Some(b'L' | b'[' | b'T')) {
                    self.walk_type_signature()?;
                }
            }
        }
        self.copy_byte()
    }

    fn walk_type_signature(&mut self) -> Step {
        match self.peek() {
            Some(b'B' | b'C' | b'D' | b'F' | b'I' | b'J' | b'S' | b'Z' | b'V') => self.copy_byte(),
            Some(b'[') => {
                self.copy_byte()?;
                self.walk_type_signature()
            }
            Some(b'T') => {
                let variable = self.take_until(b";")?;
                self.out.push_str(variable);
                self.copy_byte()
            }
            Some(b'L') => self.walk_class_type(),
            _ => Err(self.build_malformed()),
        }
    }

    fn walk_class_type(&mut self) -> Step {
        self.copy_byte()?;
        let mut name = self.take_until(b"<.;")?.to_owned();
        match self.remapper.map_internal(&name) {
            Some(mapped) => {
                self.out.push_str(mapped);
                self.changed = true;
            }
            None => self.out.push_str(&name),
        }
        loop {
            match self.peek() {
                Some(b'<') => self.walk_type_arguments()?,
                Some(b'.') => {
                    self.pos += 1;
                    let inner = self.take_until(b"<.;")?;
                    let inner_class = format!("{name}${inner}");
                    let mapped_outer = format!("{}$", self.map_type_or_keep(&name));
                    let mapped_inner = self.map_type_or_keep(&inner_class);
                    let simple = match mapped_inner.strip_prefix(&mapped_outer) {
                        Some(simple) => simple.to_owned(),
                        None => {
                            mapped_inner[mapped_inner.rfind('$').map_or(0, |d| d + 1)..].to_owned()
                        }
                    };
                    self.changed |= simple != inner;
                    self.out.push('.');
                    self.out.push_str(&simple);
                    name = inner_class;
                }
                Some(b';') => return self.copy_byte(),
                _ => return Err(self.build_malformed()),
            }
        }
    }

    /// Returns the new name of a class, or `name` when it does not change.
    fn map_type_or_keep(&self, name: &str) -> String {
        self.remapper.map_type(name).unwrap_or_else(|| name.to_owned())
    }

    fn walk_type_arguments(&mut self) -> Step {
        self.copy_byte()?;
        loop {
            match self.peek() {
                Some(b'>') => return self.copy_byte(),
                Some(b'*') => self.copy_byte()?,
                Some(b'+' | b'-') => {
                    self.copy_byte()?;
                    self.walk_type_signature()?;
                }
                Some(_) => self.walk_type_signature()?,
                None => return Err(self.build_malformed()),
            }
        }
    }
}
