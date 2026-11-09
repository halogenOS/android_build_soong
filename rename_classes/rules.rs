// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

//! The rules file of jarjar, for files whose rules are all exact class renames.

use std::collections::HashMap;
use std::fmt;
use std::str::Utf8Error;

/// Exact renames of internal class names, such as `a/b/C` to `x/a/b/C`.
pub struct Renames(HashMap<String, String>);

/// A rules file that jarjar reads as something other than exact class renames, or reads
/// differently from this reader.
#[derive(Debug)]
pub enum UnsupportedRules {
    /// jarjar reads the file as UTF-8 and stops at bytes that are not UTF-8.
    NotUtf8(Utf8Error),
    /// A wildcard rule, a `zap`, `keep` or `strip-annotation` rule, or a line that jarjar splits
    /// into other words, such as one that starts with whitespace.
    Line { number: usize, text: String },
}

impl fmt::Display for UnsupportedRules {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NotUtf8(error) => write!(f, "the rules file is not UTF-8: {error}"),
            Self::Line { number, text } => {
                write!(f, "line {number} is not an exact class rename: {text}")
            }
        }
    }
}

/// The lines of `text` as `BufferedReader.readLine` of Java reads them: `\n`, `\r` and `\r\n` end
/// a line.
fn split_java_lines(text: &str) -> impl Iterator<Item = &str> {
    text.split('\n').flat_map(|line| line.strip_suffix('\r').unwrap_or(line).split('\r'))
}

/// Whether `c` matches `\s` of a Java pattern, at which jarjar splits a line into words.
fn is_java_whitespace(c: char) -> bool {
    matches!(c, ' ' | '\t' | '\n' | '\u{b}' | '\u{c}' | '\r')
}

/// Whether `c` may be in an exact pattern. The `Wildcard` of jarjar accepts the characters of Java
/// identifiers, `*`, `-` and `.` in a pattern. This reader accepts only the ASCII ones of them,
/// without `*`, and leaves a pattern with any other to jarjar.
fn is_exact_pattern_char(c: char) -> bool {
    c.is_ascii_alphanumeric() || matches!(c, '$' | '_' | '-' | '.')
}

/// Whether jarjar matches `pattern` against exactly one class in a package and writes `result`
/// unchanged. Class names in a package contain a `.`, which member and attribute names never
/// contain.
fn is_exact(pattern: &str, result: &str) -> bool {
    pattern.chars().all(is_exact_pattern_char)
        && !result.contains('@')
        && pattern.contains('.')
        && result.contains('.')
}

impl Renames {
    /// Reads the rules as jarjar does: `#` starts a comment, words are split at whitespace, and
    /// the first rule for a name is used.
    pub fn parse(bytes: &[u8]) -> Result<Self, UnsupportedRules> {
        let text = std::str::from_utf8(bytes).map_err(UnsupportedRules::NotUtf8)?;
        let mut renames = HashMap::new();
        for (index, line) in split_java_lines(text).enumerate() {
            let content = line.split('#').next().unwrap_or_default();
            if content.is_empty() {
                continue;
            }
            let unsupported =
                || UnsupportedRules::Line { number: index + 1, text: line.to_owned() };
            // jarjar splits with `\s+`, so a line that starts with whitespace has an empty first
            // word or no words at all, and jarjar refuses it.
            if content.starts_with(is_java_whitespace) {
                return Err(unsupported());
            }
            let words: Vec<&str> =
                content.split(is_java_whitespace).filter(|word| !word.is_empty()).collect();
            match words.as_slice() {
                ["rule", pattern, result, ..] if is_exact(pattern, result) => {
                    let _first_rule_is_used = renames
                        .entry(pattern.replace('.', "/"))
                        .or_insert_with(|| result.replace('.', "/"));
                }
                _ => return Err(unsupported()),
            }
        }
        Ok(Self(renames))
    }

    /// The new internal name of a class, if a rule renames it.
    pub fn get(&self, internal_name: &str) -> Option<&str> {
        self.0.get(internal_name).map(String::as_str)
    }
}
