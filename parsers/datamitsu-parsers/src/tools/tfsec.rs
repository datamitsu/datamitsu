//! tfsec — security scanner for Terraform code.
//! Ported from the none-ls diagnostics/tfsec builtin.
//!
//! tfsec emits `{"results":[ … ]}` with `-f json`. The builtin's custom
//! `on_output` first skips any leading noise to the first `{`, JSON-decodes, then
//! maps each result: `description` -> message, `location.start_line` -> row,
//! `location.end_line` -> end_row, `rule_id` -> code, source "tfsec". The level
//! comes from the result's `severity` (`CRITICAL`/`HIGH`/`MEDIUM`/`LOW`), the
//! rule URL from the first entry of `links`. There is no column information in
//! the output.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "tfsec",
	description: "Security scanner for Terraform code",
	url: "https://github.com/aquasecurity/tfsec",
	severities: &[
		Level("CRITICAL", severity::ERROR),
		Level("HIGH", severity::ERROR),
		Level("MEDIUM", severity::WARNING),
		Level("LOW", severity::INFO),
	],
	column_unit: "",
	category: "security",
	kind: "tool",
	// Upstream runs against $DIRNAME (multiple_files), not stdin.
	operations: &[Operation {
		mode: "lint",
		args: &["-s", "-f", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	// The builtin skips any leading output before the first `{` and decodes the
	// remainder; without a `{` there is nothing parseable.
	let json = match text.find('{') {
		Some(i) => &text[i..],
		None => return Vec::new(),
	};
	let value: JsonValue = match json.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let results = match &value {
		JsonValue::Object(m) => match m.get("results") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};
	results.iter().filter_map(result_to_diag).collect()
}

fn result_to_diag(result: &JsonValue) -> Option<RawDiagnostic> {
	let map = match result {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	// With --include-passed tfsec lists the checks that passed (status 1) or
	// were ignored (2) with their severity: only a failed one is a finding.
	if matches!(map.get("status"), Some(JsonValue::Number(n)) if *n != 0.0) {
		return None;
	}
	// message = description (the one required field).
	let message = get_str(map, "description")?;
	let location = map.get("location").and_then(as_object);
	let url = match map.get("links") {
		Some(JsonValue::Array(links)) => match links.first() {
			Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
			_ => None,
		},
		_ => None,
	};

	Some(RawDiagnostic {
		message,
		row: location.and_then(|l| get_u32(l, "start_line")),
		end_row: location.and_then(|l| get_u32(l, "end_line")),
		code: get_str(map, "rule_id"),
		url,
		severity: get_str(map, "severity").and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		source: Some("tfsec".to_string()),
		file: location
			.and_then(|l| get_str(l, "filename"))
			.as_deref()
			.and_then(crate::diagnostic::exact_file_field),
		..RawDiagnostic::default()
	})
}

fn as_object(v: &JsonValue) -> Option<&std::collections::HashMap<String, JsonValue>> {
	match v {
		JsonValue::Object(m) => Some(m),
		_ => None,
	}
}

fn get_str(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_result() {
		let json = br#"{
            "results": [
                {
                    "rule_id": "aws-s3-enable-bucket-logging",
                    "description": "Bucket has logging disabled",
                    "severity": "MEDIUM",
                    "links": [
                        "https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/s3/enable-bucket-logging/",
                        "https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/s3_bucket"
                    ],
                    "location": {
                        "filename": "main.tf",
                        "start_line": 4,
                        "end_line": 6
                    }
                }
            ]
        }"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Bucket has logging disabled");
		assert_eq!(out[0].row, Some(4));
		assert_eq!(out[0].end_row, Some(6));
		assert_eq!(out[0].code.as_deref(), Some("aws-s3-enable-bucket-logging"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/s3/enable-bucket-logging/")
		);
		assert_eq!(out[0].source.as_deref(), Some("tfsec"));
		assert_eq!(out[0].col, None);
	}

	#[test]
	fn reads_all_four_levels_and_the_first_link() {
		let s = &SAMPLES[0];
		let out = parse(s.stdout, s.stderr, s.exit);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[
				Some(severity::ERROR),
				Some(severity::ERROR),
				Some(severity::WARNING),
				Some(severity::INFO)
			]
		);
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/iam/no-policy-wildcards/")
		);
		assert_eq!(out[0].code.as_deref(), Some("AVD-AWS-0057"));
		assert_eq!(
			out[0].message,
			"IAM policy document uses sensitive action 's3:*' on wildcarded resource '*'"
		);
	}

	#[test]
	fn no_level_or_url_when_the_result_has_none() {
		let stdout =
			br#"{"results":[{"description":"x","severity":"","links":[],"location":{"start_line":1,"end_line":1}}]}"#;
		let out = parse(stdout, b"", 1);
		assert_eq!(out[0].severity, None);
		assert_eq!(out[0].url, None);
		let unknown = br#"{"results":[{"description":"x","severity":"NONE"}]}"#;
		assert_eq!(parse(unknown, b"", 1)[0].severity, None);
	}

	#[test]
	fn skips_leading_noise_before_json() {
		let stdout =
			b"warning: something\n{\"results\":[{\"description\":\"x\",\"location\":{\"start_line\":1,\"end_line\":1}}]}";
		let out = parse(stdout, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "x");
	}

	#[test]
	fn null_results_yield_nothing() {
		assert!(parse(br#"{"results":null}"#, b"", 0).is_empty());
		assert!(parse(b"no json here", b"", 1).is_empty());
	}
	#[test]
	fn a_check_that_passed_or_was_ignored_is_no_finding() {
		let json = br#"{"results":[
            {"rule_id":"a","description":"passed","severity":"HIGH","status":1,"location":{"filename":"a.tf","start_line":1,"end_line":1}},
            {"rule_id":"b","description":"ignored","severity":"HIGH","status":2,"location":{"filename":"a.tf","start_line":2,"end_line":2}},
            {"rule_id":"c","description":"failed","severity":"HIGH","status":0,"location":{"filename":"infra/b.tf","start_line":3,"end_line":3}}
        ]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1, "{out:?}");
		assert_eq!(out[0].code.as_deref(), Some("c"));
		assert_eq!(out[0].file.as_deref(), Some("infra/b.tf"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{
	"results": [
		{
			"rule_id": "AVD-AWS-0057",
			"long_id": "aws-iam-no-policy-wildcards",
			"rule_description": "IAM policy should avoid use of wildcards and instead apply the principle of least privilege",
			"rule_provider": "aws",
			"rule_service": "iam",
			"impact": "Overly permissive policies may grant access to sensitive resources",
			"resolution": "Specify the exact permissions required, and to which resources they should apply instead of using wildcards.",
			"links": [
				"https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/iam/no-policy-wildcards/",
				"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/data-sources/iam_policy_document"
			],
			"description": "IAM policy document uses sensitive action 's3:*' on wildcarded resource '*'",
			"severity": "HIGH",
			"warning": false,
			"status": 0,
			"resource": "data.aws_iam_policy_document.p",
			"location": {
				"filename": "/work/tfsec/iam.tf",
				"start_line": 4,
				"end_line": 4
			}
		},
		{
			"rule_id": "AVD-AWS-0107",
			"long_id": "aws-ec2-no-public-ingress-sgr",
			"rule_description": "An ingress security group rule allows traffic from /0.",
			"rule_provider": "aws",
			"rule_service": "ec2",
			"impact": "Your port exposed to the internet",
			"resolution": "Set a more restrictive cidr range",
			"links": [
				"https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/ec2/no-public-ingress-sgr/",
				"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/security_group_rule#cidr_blocks"
			],
			"description": "Security group rule allows ingress from public internet.",
			"severity": "CRITICAL",
			"warning": false,
			"status": 0,
			"resource": "aws_security_group_rule.r",
			"location": {
				"filename": "/work/tfsec/sg.tf",
				"start_line": 7,
				"end_line": 7
			}
		},
		{
			"rule_id": "AVD-AWS-0090",
			"long_id": "aws-s3-enable-versioning",
			"rule_description": "S3 Data should be versioned",
			"rule_provider": "aws",
			"rule_service": "s3",
			"impact": "Deleted or modified data would not be recoverable",
			"resolution": "Enable versioning to protect against accidental/malicious removal or modification",
			"links": [
				"https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/s3/enable-versioning/",
				"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/s3_bucket#versioning"
			],
			"description": "Bucket does not have versioning enabled",
			"severity": "MEDIUM",
			"warning": false,
			"status": 0,
			"resource": "aws_s3_bucket.b",
			"location": {
				"filename": "/work/tfsec/s3.tf",
				"start_line": 1,
				"end_line": 3
			}
		},
		{
			"rule_id": "AVD-AWS-0089",
			"long_id": "aws-s3-enable-bucket-logging",
			"rule_description": "S3 Bucket does not have logging enabled.",
			"rule_provider": "aws",
			"rule_service": "s3",
			"impact": "There is no way to determine the access to this bucket",
			"resolution": "Add a logging block to the resource to enable access logging",
			"links": [
				"https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/s3/enable-bucket-logging/",
				"https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/s3_bucket"
			],
			"description": "Bucket does not have logging enabled",
			"severity": "LOW",
			"warning": false,
			"status": 0,
			"resource": "aws_s3_bucket.b",
			"location": {
				"filename": "/work/tfsec/s3.tf",
				"start_line": 1,
				"end_line": 3
			}
		}
	]
}
"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"{
	"results": [
		{
			"rule_id": "AVD-AWS-0089",
			"long_id": "aws-s3-enable-bucket-logging",
			"rule_description": "S3 Bucket does not have logging enabled.",
			"rule_provider": "aws",
			"rule_service": "s3",
			"impact": "There is no way to determine the access to this bucket",
			"resolution": "Add a logging block to the resource to enable access logging",
			"links": [
				"https://aquasecurity.github.io/tfsec/v1.28.13/checks/aws/s3/enable-bucket-logging/"
			],
			"description": "Bucket does not have logging enabled",
			"severity": "LOW",
			"warning": false,
			"status": 0,
			"resource": "aws_s3_bucket.b",
			"location": {
				"filename": "/work/tfsec/s3.tf",
				"start_line": 1,
				"end_line": 3
			}
		}
	]
}
"#,
		stderr: b"",
		exit: 0,
	},
];
