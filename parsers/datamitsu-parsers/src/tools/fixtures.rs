//! Recorded-output fixtures for every parser the reference configuration wires
//! (`fixtures/<parser>/`). Each parser has a pair recorded from the real tool: a
//! clean run, which must yield no diagnostic, and a finding-bearing run, whose
//! diagnostics are asserted field by field. A parser that answers everything
//! with nothing fails the second, and a tool whose output format changed shows
//! up when its fixture is recorded again with the tool bump
//! (`fixtures/README.md`).

use super::dispatch;
use crate::diagnostic::RawDiagnostic;
use crate::severity;

struct Recording {
	stdout: &'static [u8],
	stderr: &'static [u8],
	exit: &'static str,
}

macro_rules! recording {
	($key:literal, $case:literal) => {
		Recording {
			stdout: include_bytes!(concat!("../../fixtures/", $key, "/", $case, ".stdout")),
			stderr: include_bytes!(concat!("../../fixtures/", $key, "/", $case, ".stderr")),
			exit: include_str!(concat!("../../fixtures/", $key, "/", $case, ".exit")),
		}
	};
}

fn exit_code(r: &Recording) -> i32 {
	r.exit.trim().parse().expect("a recorded exit code")
}

/// Every recorded run, clean and finding-bearing, keyed by parser: the severity
/// contract and the position audit run over each of them (`crate::contract`).
pub(crate) fn recorded() -> Vec<(&'static str, crate::contract::Sample)> {
	macro_rules! pair {
		($key:literal) => {
			[recording!($key, "clean"), recording!($key, "findings")].map(|r| {
				(
					$key,
					crate::contract::Sample {
						stdout: r.stdout,
						stderr: r.stderr,
						exit: exit_code(&r),
					},
				)
			})
		};
	}
	[
		pair!("actionlint"),
		pair!("checkmake"),
		pair!("cspell"),
		pair!("dclint"),
		pair!("dotenv_linter"),
		pair!("eslint"),
		pair!("golangci_lint"),
		pair!("hadolint"),
		pair!("harper_cli"),
		pair!("protolint"),
		pair!("tsc"),
		pair!("vale"),
		pair!("yamllint"),
	]
	.concat()
}

fn parse(key: &str, r: &Recording) -> Vec<RawDiagnostic> {
	dispatch(key, r.stdout, r.stderr, exit_code(r)).expect("a parser the module dispatches")
}

fn assert_findings(key: &str, r: &Recording, want: &[RawDiagnostic]) {
	let got = parse(key, r);
	assert!(!got.is_empty(), "{key}: the recorded findings parsed to nothing");
	assert_eq!(got.len(), want.len(), "{key}: {got:#?}");
	for (i, (g, w)) in got.iter().zip(want).enumerate() {
		assert_eq!(g, w, "{key}: diagnostic {i}");
	}
	let json = crate::dispatch(key, r.stdout, r.stderr, exit_code(r));
	assert!(json.starts_with("[{") && json.ends_with("}]"), "{key}: {json}");
}

#[test]
fn clean_output_yields_nothing() {
	let clean = [
		("actionlint", recording!("actionlint", "clean")),
		("checkmake", recording!("checkmake", "clean")),
		("cspell", recording!("cspell", "clean")),
		("dclint", recording!("dclint", "clean")),
		("dotenv_linter", recording!("dotenv_linter", "clean")),
		("eslint", recording!("eslint", "clean")),
		("golangci_lint", recording!("golangci_lint", "clean")),
		("hadolint", recording!("hadolint", "clean")),
		("harper_cli", recording!("harper_cli", "clean")),
		("protolint", recording!("protolint", "clean")),
		("tsc", recording!("tsc", "clean")),
		("vale", recording!("vale", "clean")),
		("yamllint", recording!("yamllint", "clean")),
	];
	for (key, r) in &clean {
		assert_eq!(exit_code(r), 0, "{key}: a clean recording exits 0");
		assert_eq!(parse(key, r), Vec::<RawDiagnostic>::new(), "{key}");
		assert_eq!(crate::dispatch(key, r.stdout, r.stderr, 0), "[]", "{key}");
	}
}

#[test]
fn actionlint_findings() {
	assert_findings(
		"actionlint",
		&recording!("actionlint", "findings"),
		&[
			RawDiagnostic {
				message: "job \"build\" needs job \"missing\" which does not exist in this workflow".into(),
				row: Some(4),
				col: Some(3),
				end_col: Some(9),
				source: Some("actionlint".into()),
				code: Some("job-needs".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "\"github.event.issue.title\" is potentially untrusted. avoid using it directly in inline scripts. instead, pass it through an environment variable. see https://docs.github.com/en/actions/reference/security/secure-use#good-practices-for-mitigating-script-injection-attacks for more details".into(),
				row: Some(7),
				col: Some(23),
				end_col: Some(47),
				source: Some("actionlint".into()),
				code: Some("expression".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "input \"foo\" is not defined in action \"actions/checkout@v4\". available inputs are \"clean\", \"fetch-depth\", \"fetch-tags\", \"filter\", \"github-server-url\", \"lfs\", \"path\", \"persist-credentials\", \"ref\", \"repository\", \"set-safe-directory\", \"show-progress\", \"sparse-checkout\", \"sparse-checkout-cone-mode\", \"ssh-key\", \"ssh-known-hosts\", \"ssh-strict\", \"ssh-user\", \"submodules\", \"token\"".into(),
				row: Some(10),
				col: Some(11),
				end_col: Some(15),
				source: Some("actionlint".into()),
				code: Some("action".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn checkmake_findings() {
	assert_findings(
		"checkmake",
		&recording!("checkmake", "findings"),
		&[
			RawDiagnostic {
				message: "Required target \"all\" is missing from the Makefile.".into(),
				row: Some(1),
				code: Some("minphony".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Required target \"clean\" is missing from the Makefile.".into(),
				row: Some(1),
				code: Some("minphony".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Required target \"test\" is missing from the Makefile.".into(),
				row: Some(1),
				code: Some("minphony".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn cspell_findings() {
	assert_findings(
		"cspell",
		&recording!("cspell", "findings"),
		&[
			RawDiagnostic {
				message: "Unknown word (sentense) fix: (sentence)".into(),
				row: Some(1),
				col: Some(6),
				file: Some("findings.txt".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Unknown word (mispelled) fix: (misspelled)".into(),
				row: Some(1),
				col: Some(21),
				file: Some("findings.txt".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn dclint_findings() {
	assert_findings(
		"dclint",
		&recording!("dclint", "findings"),
		&[
			RawDiagnostic {
				message: "Service \"web\" is exporting port \"8080:80\" without specifying the interface to listen on.".into(),
				row: Some(5),
				col: Some(1),
				severity: Some(severity::ERROR),
				code: Some("no-unbound-port-interfaces".into()),
				url: Some("https://github.com/zavoloklom/docker-compose-linter/blob/main/docs/rules/no-unbound-port-interfaces-rule.md".into()),
				file: Some("findings.yml".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "The \"version\" field should not be present.".into(),
				row: Some(1),
				col: Some(1),
				severity: Some(severity::ERROR),
				code: Some("no-version-field".into()),
				url: Some("https://github.com/zavoloklom/docker-compose-linter/blob/main/docs/rules/no-version-field-rule.md".into()),
				file: Some("findings.yml".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "The \"name\" field should be present.".into(),
				row: Some(1),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("require-project-name-field".into()),
				url: Some("https://github.com/zavoloklom/docker-compose-linter/blob/main/docs/rules/require-project-name-field-rule.md".into()),
				file: Some("findings.yml".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Ports in `ports` and `expose` sections should be enclosed in quotes.".into(),
				row: Some(6),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("require-quotes-in-ports".into()),
				url: Some("https://github.com/zavoloklom/docker-compose-linter/blob/main/docs/rules/require-quotes-in-ports-rule.md".into()),
				file: Some("findings.yml".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Service \"web\" is using the image \"nginx\", which does not have a concrete version tag. Specify a concrete version tag.".into(),
				row: Some(4),
				col: Some(1),
				severity: Some(severity::ERROR),
				code: Some("service-image-require-explicit-tag".into()),
				url: Some("https://github.com/zavoloklom/docker-compose-linter/blob/main/docs/rules/service-image-require-explicit-tag-rule.md".into()),
				file: Some("findings.yml".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn dotenv_linter_findings() {
	assert_findings(
		"dotenv_linter",
		&recording!("dotenv_linter", "findings"),
		&[
			RawDiagnostic {
				message: "The b key should be in uppercase".into(),
				row: Some(1),
				code: Some("LowercaseKey".into()),
				file: Some("findings.env".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "The line has spaces around equal sign".into(),
				row: Some(2),
				code: Some("SpaceCharacter".into()),
				file: Some("findings.env".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "The A  key should go before the b key".into(),
				row: Some(2),
				code: Some("UnorderedKey".into()),
				file: Some("findings.env".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "The A key should go before the A  key".into(),
				row: Some(3),
				code: Some("UnorderedKey".into()),
				file: Some("findings.env".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn eslint_findings() {
	assert_findings(
		"eslint",
		&recording!("eslint", "findings"),
		&[
			RawDiagnostic {
				message: "'unused' is assigned a value but never used.".into(),
				row: Some(1),
				col: Some(7),
				end_row: Some(1),
				end_col: Some(13),
				severity: Some(severity::ERROR),
				code: Some("no-unused-vars".into()),
				file: Some("/work/eslint/findings.js".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "'foo' is not defined.".into(),
				row: Some(2),
				col: Some(1),
				end_row: Some(2),
				end_col: Some(4),
				severity: Some(severity::ERROR),
				code: Some("no-undef".into()),
				file: Some("/work/eslint/findings.js".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn golangci_lint_findings() {
	assert_findings(
		"golangci_lint",
		&recording!("golangci_lint", "findings"),
		&[
			RawDiagnostic {
				message: "Error return value of `os.Remove` is not checked".into(),
				row: Some(12),
				col: Some(11),
				source: Some("golangci-lint".into()),
				code: Some("errcheck".into()),
				file: Some("main.go".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "ineffectual assignment to x".into(),
				row: Some(9),
				col: Some(2),
				source: Some("golangci-lint".into()),
				code: Some("ineffassign".into()),
				file: Some("main.go".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn hadolint_findings() {
	assert_findings(
		"hadolint",
		&recording!("hadolint", "findings"),
		&[
			RawDiagnostic {
				message: "Always tag the version of an image explicitly".into(),
				row: Some(1),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("DL3006".into()),
				file: Some("findings.Dockerfile".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Avoid additional packages by specifying `--no-install-recommends`".into(),
				row: Some(2),
				col: Some(1),
				severity: Some(severity::INFO),
				code: Some("DL3015".into()),
				file: Some("findings.Dockerfile".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Pin versions in apt get install. Instead of `apt-get install <package>` use `apt-get install <package>=<version>`".into(),
				row: Some(2),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("DL3008".into()),
				file: Some("findings.Dockerfile".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Use the `-y` switch to avoid manual input `apt-get -y install <package>`".into(),
				row: Some(2),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("DL3014".into()),
				file: Some("findings.Dockerfile".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Use WORKDIR to switch to a directory".into(),
				row: Some(3),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("DL3003".into()),
				file: Some("findings.Dockerfile".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Multiple consecutive `RUN` instructions. Consider consolidation.".into(),
				row: Some(3),
				col: Some(1),
				severity: Some(severity::INFO),
				code: Some("DL3059".into()),
				file: Some("findings.Dockerfile".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Use 'cd ... || exit' or 'cd ... || return' in case cd fails.".into(),
				row: Some(3),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("SC2164".into()),
				file: Some("findings.Dockerfile".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn harper_cli_findings() {
	assert_findings(
		"harper_cli",
		&recording!("harper_cli", "findings"),
		&[RawDiagnostic {
			message: "Incorrect indefinite article.".into(),
			row: Some(1),
			col: Some(9),
			code: Some("Miscellaneous::AnA".into()),
			file: Some("findings.md".into()),
			..RawDiagnostic::default()
		}],
	);
}

#[test]
fn protolint_findings() {
	assert_findings(
		"protolint",
		&recording!("protolint", "findings"),
		&[
			RawDiagnostic {
				message: "Field name \"Text\" must be underscore_separated_names like \"text\"".into(),
				row: Some(6),
				col: Some(3),
				severity: Some(severity::ERROR),
				source: Some("protolint".into()),
				code: Some("FIELD_NAMES_LOWER_SNAKE_CASE".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Message name \"greeting\" must be UpperCamelCase like \"Greeting\"".into(),
				row: Some(5),
				col: Some(1),
				severity: Some(severity::ERROR),
				source: Some("protolint".into()),
				code: Some("MESSAGE_NAMES_UPPER_CAMEL_CASE".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn tsc_findings() {
	assert_findings(
		"tsc",
		&recording!("tsc", "findings"),
		&[
			RawDiagnostic {
				message: "Type 'string' is not assignable to type 'number'.".into(),
				row: Some(1),
				col: Some(14),
				severity: Some(severity::ERROR),
				code: Some("TS2322".into()),
				file: Some("index.ts".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Cannot find name 'missing'.".into(),
				row: Some(2),
				col: Some(26),
				severity: Some(severity::ERROR),
				code: Some("TS2304".into()),
				file: Some("index.ts".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn vale_findings() {
	assert_findings(
		"vale",
		&recording!("vale", "findings"),
		&[
			RawDiagnostic {
				message: "'is' is repeated!".into(),
				row: Some(1),
				col: Some(6),
				end_col: Some(11),
				severity: Some(severity::ERROR),
				code: Some("Vale.Repetition".into()),
				file: Some("findings.md".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "Did you really mean 'mispeled'?".into(),
				row: Some(1),
				col: Some(14),
				end_col: Some(22),
				severity: Some(severity::ERROR),
				code: Some("Vale.Spelling".into()),
				file: Some("findings.md".into()),
				..RawDiagnostic::default()
			},
		],
	);
}

#[test]
fn yamllint_findings() {
	assert_findings(
		"yamllint",
		&recording!("yamllint", "findings"),
		&[
			RawDiagnostic {
				message: "missing document start \"---\"".into(),
				row: Some(1),
				col: Some(1),
				severity: Some(severity::WARNING),
				code: Some("document-start".into()),
				file: Some("findings.yaml".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "too many blank lines (3 > 2)".into(),
				row: Some(4),
				col: Some(1),
				severity: Some(severity::ERROR),
				code: Some("empty-lines".into()),
				file: Some("findings.yaml".into()),
				..RawDiagnostic::default()
			},
			RawDiagnostic {
				message: "too many spaces after colon".into(),
				row: Some(5),
				col: Some(5),
				severity: Some(severity::ERROR),
				code: Some("colons".into()),
				file: Some("findings.yaml".into()),
				..RawDiagnostic::default()
			},
		],
	);
}
