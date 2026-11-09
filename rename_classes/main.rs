// SPDX-FileCopyrightText: The halogenOS Project
// SPDX-License-Identifier: Apache-2.0

//! Renames classes in a jar by a jarjar rules file whose rules are all exact class renames, and
//! writes the jar that `jarjar process` writes for the same rules. A rules file with any other
//! rule ends the run with exit status 3, so the caller runs jarjar for that file.

#![forbid(unsafe_code)]
#![deny(unused_results, clippy::let_underscore_must_use, clippy::all, clippy::pedantic)]

mod classfile;
mod jar;
mod remap;
mod rules;

use clap::{CommandFactory, FromArgMatches, Parser};
use jar::JarError;
use remap::Remapper;
use rules::{Renames, UnsupportedRules};
use std::error::Error;
use std::path::PathBuf;
use std::process::ExitCode;
use std::{fmt, fs, io};

const ABOUT: &str =
    "Writes OUTPUT_JAR with the classes of INPUT_JAR renamed by the rules in RULES_FILE.";

const DESCRIPTION: &str = "\
Writes OUTPUT_JAR with the classes of INPUT_JAR renamed by the rules in RULES_FILE. OUTPUT_JAR
has the entries, names and modification times that `jarjar process` writes for the same rules.
Methods have `max_stack` and `max_locals` as the compiler wrote them, where jarjar computes them
again.

Every rule must rename one class exactly, such as `rule a.b.C x.a.b.C`. Any other rule, such as
a wildcard rule, a zap rule or a keep rule, and any line that jarjar reads differently, such as
one that starts with whitespace, ends the run before OUTPUT_JAR is created, with exit status 3,
so the caller runs jarjar.";

/// The command line of the tool.
#[derive(Parser)]
#[command(name = "rename_classes", about = ABOUT, long_about = DESCRIPTION)]
struct Arguments {
    #[arg(
        long,
        help = "Remove @UnsupportedAppUsage from every renamed class, as jarjar does with \
                -DremoveAndroidCompatAnnotations=true."
    )]
    remove_android_compat_annotations: bool,
    #[arg(help = "The jar whose classes are renamed.")]
    input_jar: PathBuf,
    #[arg(help = "The jar to write.")]
    output_jar: PathBuf,
    #[arg(help = "The jarjar rules file.")]
    rules_file: PathBuf,
}

/// The exit statuses of the tool.
#[derive(Clone, Copy)]
enum Status {
    Success,
    Failed,
    /// clap exits with this status when the arguments do not match the usage.
    WrongArguments,
    /// The rule renameClassesOrJarjar in java/builder.go of Soong runs jarjar on this status.
    NeedsJarjar,
}

impl Status {
    const ALL: [Self; 4] = [Self::Success, Self::Failed, Self::WrongArguments, Self::NeedsJarjar];

    fn code(self) -> u8 {
        match self {
            Self::Success => 0,
            Self::Failed => 1,
            Self::WrongArguments => 2,
            Self::NeedsJarjar => 3,
        }
    }

    fn meaning(self) -> &'static str {
        match self {
            Self::Success => "OUTPUT_JAR is written.",
            Self::Failed => {
                "The renaming failed, for example on a jar or class file it cannot read."
            }
            Self::WrongArguments => "The arguments do not match the usage.",
            Self::NeedsJarjar => {
                "RULES_FILE needs jarjar: it contains a rule that is not an exact class rename, or a \
                 line that jarjar reads differently. OUTPUT_JAR is not created."
            }
        }
    }
}

impl From<Status> for ExitCode {
    fn from(status: Status) -> Self {
        Self::from(status.code())
    }
}

/// Describes every exit status, for the help text.
fn describe_exit_statuses() -> String {
    let lines: Vec<String> = Status::ALL
        .into_iter()
        .map(|status| format!("  {}  {}", status.code(), status.meaning()))
        .collect();
    format!("Exit status:\n{}", lines.join("\n"))
}

#[derive(Debug)]
enum RunError {
    ReadRules(io::Error),
    RenameJar(JarError),
}

impl fmt::Display for RunError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::ReadRules(_) => write!(f, "cannot read the rules file"),
            Self::RenameJar(_) => write!(f, "cannot rename the jar"),
        }
    }
}

impl Error for RunError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::ReadRules(error) => Some(error),
            Self::RenameJar(error) => Some(error),
        }
    }
}

/// What a run did.
enum Outcome {
    Written,
    /// The rules file needs jarjar, and the output jar is not created.
    NeedsJarjar(UnsupportedRules),
}

fn run(arguments: &Arguments) -> Result<Outcome, RunError> {
    let rules = fs::read(&arguments.rules_file).map_err(RunError::ReadRules)?;
    let renames = match Renames::parse(&rules) {
        Ok(renames) => renames,
        Err(unsupported) => return Ok(Outcome::NeedsJarjar(unsupported)),
    };
    jar::rename(
        &arguments.input_jar,
        &arguments.output_jar,
        &Remapper::new(renames),
        arguments.remove_android_compat_annotations,
    )
    .map_err(RunError::RenameJar)?;
    Ok(Outcome::Written)
}

/// Prints the error with each error that caused it.
fn print_error(error: &(dyn Error + 'static)) {
    let causes: Vec<String> = std::iter::successors(Some(error), |&error| error.source())
        .map(ToString::to_string)
        .collect();
    eprintln!("rename_classes: {}", causes.join(": "));
}

fn main() -> ExitCode {
    let matches = Arguments::command().after_help(describe_exit_statuses()).get_matches();
    let arguments = Arguments::from_arg_matches(&matches).unwrap_or_else(|error| error.exit());
    let status = match run(&arguments) {
        Ok(Outcome::Written) => Status::Success,
        // jarjar applies the rules file, or reports what it refuses in it.
        Ok(Outcome::NeedsJarjar(_rules_left_to_jarjar)) => Status::NeedsJarjar,
        Err(error) => {
            print_error(&error);
            Status::Failed
        }
    };
    status.into()
}
