//! Real tools printing a standard format on request (`fixtures/<format>/`),
//! recorded clean and with findings. Each recording must be recognized by its
//! format parser and by the sniffer, which must pick that format.

use crate::diagnostic::RawDiagnostic;
use crate::severity;

struct Recording {
	stdout: &'static [u8],
	stderr: &'static [u8],
	exit: &'static str,
}

macro_rules! recording {
	($key:literal, $case:literal) => {
		(
			$key,
			Recording {
				stdout: include_bytes!(concat!("../../fixtures/", $key, "/", $case, ".stdout")),
				stderr: include_bytes!(concat!("../../fixtures/", $key, "/", $case, ".stderr")),
				exit: include_str!(concat!("../../fixtures/", $key, "/", $case, ".exit")),
			},
		)
	};
}

fn exit_code(r: &Recording) -> i32 {
	r.exit.trim().parse().expect("a recorded exit code")
}

fn clean() -> Vec<(&'static str, Recording)> {
	vec![
		recording!("checkstyle-xml", "hadolint-clean"),
		recording!("checkstyle-xml", "shellcheck-clean"),
		recording!("gcc", "typos-clean"),
		recording!("sarif", "ruff-clean"),
	]
}

fn findings() -> Vec<(&'static str, Recording)> {
	vec![
		recording!("azure-logissue", "ruff-findings"),
		recording!("checkstyle-xml", "hadolint-findings"),
		recording!("checkstyle-xml", "oxlint-findings"),
		recording!("checkstyle-xml", "shellcheck-findings"),
		recording!("checkstyle-xml", "tflint-findings"),
		recording!("codeclimate", "ruff-findings"),
		recording!("gcc", "shellcheck-findings"),
		recording!("gcc", "typos-findings"),
		recording!("github-annotations", "ruff-findings"),
		recording!("junit-xml", "golangci-lint-findings"),
		recording!("junit-xml", "ruff-findings"),
		recording!("sarif", "ruff-findings"),
	]
}

/// Every recording, keyed by format, for the checks every parser passes
/// (`crate::contract`).
pub(crate) fn recorded() -> Vec<(&'static str, crate::contract::Sample)> {
	clean()
		.into_iter()
		.chain(findings())
		.map(|(key, r)| {
			let exit = exit_code(&r);
			(
				key,
				crate::contract::Sample {
					stdout: r.stdout,
					stderr: r.stderr,
					exit,
				},
			)
		})
		.collect()
}

/// The findings the declared format parser reads from `r`, which the sniffer
/// must read the same way unless the recording printed nothing to sniff.
fn parse(key: &str, r: &Recording) -> Vec<RawDiagnostic> {
	let answer = super::dispatch(key, r.stdout, r.stderr, exit_code(r)).expect("a format parser");
	assert!(answer.recognized, "{key}: not recognized");
	assert_eq!(answer.format, key);
	let sniffed = crate::fallback::sniff(r.stdout, r.stderr, exit_code(r));
	if r.stdout.is_empty() && r.stderr.is_empty() {
		assert!(!sniffed.recognized, "{key}: the sniffer recognized nothing printed");
		return answer.diagnostics;
	}
	assert_eq!(
		(sniffed.recognized, sniffed.format.as_str()),
		(true, key),
		"the sniffer's pick"
	);
	assert_eq!(sniffed.diagnostics, answer.diagnostics, "{key}: the sniffer's findings");
	answer.diagnostics
}

fn find<'a>(diags: &'a [RawDiagnostic], code: &str) -> &'a RawDiagnostic {
	diags
		.iter()
		.find(|d| d.code.as_deref() == Some(code))
		.unwrap_or_else(|| panic!("no finding with code {code}: {diags:#?}"))
}

#[test]
fn a_clean_recording_is_recognized_and_empty() {
	for (key, r) in &clean() {
		assert_eq!(exit_code(r), 0, "{key}");
		assert_eq!(parse(key, r), Vec::new(), "{key}");
	}
}

#[test]
fn every_findings_recording_parses() {
	for (key, r) in &findings() {
		assert!(
			!parse(key, r).is_empty(),
			"{key}: the recorded findings parsed to nothing"
		);
	}
}

#[test]
fn shellcheck_checkstyle() {
	let (key, r) = recording!("checkstyle-xml", "shellcheck-findings");
	let diags = parse(key, &r);
	assert_eq!(diags.len(), 2);
	let d = find(&diags, "ShellCheck.SC2086");
	assert_eq!((d.file.as_deref(), d.row, d.col), (Some("bad.sh"), Some(2), Some(6)));
	assert_eq!(d.severity, Some(severity::INFO));
	assert_eq!(
		find(&diags, "ShellCheck.SC2034").message,
		"x appears unused. Verify use (or export if used externally)."
	);
}

#[test]
fn hadolint_checkstyle() {
	let (key, r) = recording!("checkstyle-xml", "hadolint-findings");
	let diags = parse(key, &r);
	assert_eq!(diags.len(), 3);
	let d = find(&diags, "DL3008");
	assert_eq!(
		(d.file.as_deref(), d.row, d.severity),
		(Some("Dockerfile"), Some(2), Some(severity::WARNING))
	);
	assert!(
		d.message.contains("`apt-get install <package>=<version>`"),
		"{}",
		d.message
	);
}

#[test]
fn shellcheck_gcc() {
	let (key, r) = recording!("gcc", "shellcheck-findings");
	let diags = parse(key, &r);
	let d = find(&diags, "SC2086");
	assert_eq!(
		(d.file.as_deref(), d.row, d.col, d.severity),
		(Some("bad.sh"), Some(2), Some(6), Some(severity::INFO))
	);
	assert_eq!(find(&diags, "SC2034").severity, Some(severity::WARNING));
}

#[test]
fn typos_brief() {
	let (key, r) = recording!("gcc", "typos-findings");
	let diags = parse(key, &r);
	assert_eq!(diags.len(), 2);
	assert_eq!(
		(diags[1].file.as_deref(), diags[1].row, diags[1].col),
		(Some("typo.md"), Some(1), Some(5))
	);
	assert_eq!(diags[1].severity, Some(severity::ERROR));
	assert_eq!(diags[1].message, "`recieve` should be `receive`");
}

#[test]
fn ruff_sarif() {
	let (key, r) = recording!("sarif", "ruff-findings");
	let d = &parse(key, &r)[0];
	assert_eq!(
		(d.file.as_deref(), d.row, d.col, d.end_col),
		(Some("/work/bad.py"), Some(1), Some(8), Some(10))
	);
	assert_eq!((d.code.as_deref(), d.severity), (Some("F401"), Some(severity::ERROR)));
	assert!(
		d.url.as_deref().is_some_and(|u| u.contains("unused-import")),
		"{:?}",
		d.url
	);
}

#[test]
fn ruff_codeclimate() {
	let (key, r) = recording!("codeclimate", "ruff-findings");
	let d = &parse(key, &r)[0];
	assert_eq!(
		(d.file.as_deref(), d.row, d.col, d.end_row, d.end_col),
		(Some("bad.py"), Some(1), Some(8), Some(1), Some(10))
	);
	assert_eq!((d.code.as_deref(), d.severity), (Some("F401"), Some(severity::ERROR)));
}

#[test]
fn ruff_github_annotations() {
	let (key, r) = recording!("github-annotations", "ruff-findings");
	let d = &parse(key, &r)[0];
	assert_eq!(
		(d.file.as_deref(), d.row, d.col, d.end_col),
		(Some("/work/bad.py"), Some(1), Some(8), Some(10))
	);
	assert_eq!(
		(d.code.as_deref(), d.severity),
		(Some("ruff (F401)"), Some(severity::ERROR))
	);
	assert!(d.message.contains('\n'), "{:?}", d.message);
}

#[test]
fn ruff_azure_logissue() {
	let (key, r) = recording!("azure-logissue", "ruff-findings");
	let d = &parse(key, &r)[0];
	assert_eq!(
		(d.file.as_deref(), d.row, d.col),
		(Some("/work/bad.py"), Some(1), Some(8))
	);
	assert_eq!((d.code.as_deref(), d.severity), (Some("F401"), Some(severity::ERROR)));
}

#[test]
fn ruff_junit() {
	let (key, r) = recording!("junit-xml", "ruff-findings");
	let d = &parse(key, &r)[0];
	assert_eq!((d.file.as_deref(), d.row, d.col), (None, Some(1), Some(8)));
	assert_eq!(d.code.as_deref(), Some("org.ruff.F401"));
	assert_eq!(d.message, "/work/bad: `os` imported but unused");
}

#[test]
fn tflint_checkstyle() {
	let (key, r) = recording!("checkstyle-xml", "tflint-findings");
	let diags = parse(key, &r);
	assert_eq!(diags.len(), 3);
	let d = find(&diags, "terraform_deprecated_interpolation");
	assert_eq!((d.file.as_deref(), d.row, d.col), (Some("main.tf"), Some(3), Some(11)));
	assert_eq!(d.severity, Some(severity::WARNING));
	assert!(
		d.url
			.as_deref()
			.is_some_and(|u| u.contains("terraform_deprecated_interpolation")),
		"{:?}",
		d.url
	);
	assert_eq!(
		find(&diags, "terraform_required_version").message,
		"terraform \"required_version\" attribute is required"
	);
}

#[test]
fn oxlint_checkstyle() {
	let (key, r) = recording!("checkstyle-xml", "oxlint-findings");
	let diags = parse(key, &r);
	assert_eq!(diags.len(), 2);
	let d = find(&diags, "eslint(no-debugger)");
	assert_eq!(
		(d.file.as_deref(), d.row, d.col, d.severity),
		(Some("/work/a.js"), Some(2), Some(1), Some(severity::ERROR))
	);
}

#[test]
fn golangci_lint_junit() {
	let (key, r) = recording!("junit-xml", "golangci-lint-findings");
	let d = &parse(key, &r)[0];
	assert_eq!((d.file.as_deref(), d.row, d.col), (Some("x.go"), Some(6), Some(11)));
	assert_eq!(d.code.as_deref(), Some("errcheck"));
	assert_eq!(d.severity, None);
	assert_eq!(d.message, "x.go:6:11: Error return value of `os.Remove` is not checked");
}
