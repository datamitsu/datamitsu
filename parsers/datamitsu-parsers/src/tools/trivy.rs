//! trivy — find misconfigurations and vulnerabilities. Ported from the none-ls
//! diagnostics/trivy builtin.
//!
//! `trivy config --format json` emits a nested object: `Results[]` each holding a
//! `Misconfigurations[]` array, where every misconfiguration carries an `ID`
//! (code), `Title` (message), `Severity`, `PrimaryURL` (the check's page) and a
//! `CauseMetadata` with 1-based `StartLine`/`EndLine`; trivy prints no column. A
//! `Severity` of `UNKNOWN` is no level. The Lua reads from stderr (trivy
//! historically printed its JSON there), so we accept the JSON from either
//! stream.
use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "trivy",
	description: "Find misconfigurations and vulnerabilities",
	url: "https://github.com/aquasecurity/trivy",
	severities: &[
		Level("CRITICAL", severity::ERROR),
		Level("HIGH", severity::ERROR),
		Level("MEDIUM", severity::WARNING),
		Level("LOW", severity::INFO),
	],
	column_unit: "",
	category: "security",
	kind: "tool",
	// The builtin runs trivy against a directory (`.`), not stdin or a temp file.
	operations: &[Operation {
		mode: "lint",
		args: &["config", "--format", "json", "--quiet", "."],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true historically; modern trivy prints to stdout. Try stdout
	// first, fall back to stderr.
	let mut out = parse_stream(stdout);
	if out.is_empty() {
		out = parse_stream(stderr);
	}
	out
}

fn parse_stream(bytes: &[u8]) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(bytes);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let mut diags = Vec::new();
	let Some(results) = get(&value, "Results") else {
		return diags;
	};
	let results = match results {
		JsonValue::Array(items) => items,
		_ => return diags,
	};
	for result in results {
		let Some(JsonValue::Array(miscfgs)) = get(result, "Misconfigurations") else {
			continue;
		};
		for m in miscfgs {
			if let Some(d) = from_misconfiguration(m) {
				diags.push(d);
			}
		}
	}
	diags
}

fn from_misconfiguration(m: &JsonValue) -> Option<RawDiagnostic> {
	// Title is the message; the builtin maps it unconditionally (message is
	// mandatory), so skip an entry without one.
	let message = get_str(m, "Title")?;
	let cause = get(m, "CauseMetadata");
	// A cause trivy could not place has line 0: no position.
	let line = |key: &str| cause.and_then(|c| get_u32(c, key)).filter(|&n| n > 0);
	Some(RawDiagnostic {
		message,
		row: line("StartLine"),
		end_row: line("EndLine"),
		severity: get_str(m, "Severity").and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		source: Some("trivy".to_string()),
		code: get_str(m, "ID"),
		url: get_str(m, "PrimaryURL").filter(|u| !u.is_empty()),
		..RawDiagnostic::default()
	})
}

fn get<'a>(value: &'a JsonValue, key: &str) -> Option<&'a JsonValue> {
	match value {
		JsonValue::Object(map) => map.get(key),
		_ => None,
	}
}

fn get_str(value: &JsonValue, key: &str) -> Option<String> {
	match get(value, key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32(value: &JsonValue, key: &str) -> Option<u32> {
	match get(value, key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	const SAMPLE: &[u8] = br#"{
      "Results": [
        {
          "Target": "main.tf",
          "Misconfigurations": [
            {
              "ID": "AVD-AWS-0086",
              "Title": "S3 Bucket has block public ACLs disabled",
              "Severity": "HIGH",
              "PrimaryURL": "https://avd.aquasec.com/misconfig/avd-aws-0086",
              "CauseMetadata": { "StartLine": 12, "EndLine": 18 }
            },
            {
              "ID": "AVD-AWS-0132",
              "Title": "S3 encryption should use Customer Managed Keys",
              "Severity": "LOW",
              "CauseMetadata": { "StartLine": 3, "EndLine": 3 }
            }
          ]
        }
      ]
    }"#;

	#[test]
	fn parses_nested_misconfigurations() {
		let out = parse(SAMPLE, b"", 0);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "S3 Bucket has block public ACLs disabled");
		assert_eq!(out[0].code.as_deref(), Some("AVD-AWS-0086"));
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].end_row, Some(18));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("trivy"));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://avd.aquasec.com/misconfig/avd-aws-0086")
		);

		assert_eq!(out[1].severity, Some(severity::INFO));
		assert_eq!(out[1].code.as_deref(), Some("AVD-AWS-0132"));
		assert_eq!(out[1].url, None);
	}

	#[test]
	fn reads_every_printed_level() {
		let s = &SAMPLES[0];
		let levels: Vec<_> = parse(s.stdout, s.stderr, s.exit).iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[
				Some(severity::ERROR),
				Some(severity::WARNING),
				Some(severity::INFO),
				Some(severity::ERROR)
			]
		);
	}

	#[test]
	fn unknown_severity_and_line_zero_are_absent() {
		let json = br#"{"Results":[{"Misconfigurations":[
            {"ID":"X","Title":"t","Severity":"UNKNOWN","CauseMetadata":{"StartLine":0,"EndLine":0}}
        ]}]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out[0].severity, None);
		assert_eq!((out[0].row, out[0].end_row), (None, None));
	}

	#[test]
	fn falls_back_to_stderr() {
		let out = parse(b"", SAMPLE, 0);
		assert_eq!(out.len(), 2);
	}

	#[test]
	fn no_results_yields_nothing() {
		assert!(parse(br#"{"Results":[]}"#, b"", 0).is_empty());
		assert!(parse(b"not json", b"", 0).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{
  "SchemaVersion": 2,
  "Trivy": {
    "Version": "0.74.0"
  },
  "ArtifactName": ".",
  "ArtifactType": "filesystem",
  "Results": [
    {
      "Target": ".",
      "Class": "config",
      "Type": "terraform",
      "MisconfSummary": {
        "Successes": 0,
        "Failures": 0
      }
    },
    {
      "Target": "main.tf",
      "Class": "config",
      "Type": "terraform",
      "MisconfSummary": {
        "Successes": 0,
        "Failures": 4
      },
      "Misconfigurations": [
        {
          "Type": "Terraform Security Check",
          "ID": "AWS-0086",
          "Title": "S3 Access block should block public ACL",
          "Description": "S3 buckets should block public ACLs on buckets and any objects they contain. By blocking, PUTs with fail if the object has any public ACL a.\n",
          "Message": "No public access block so not blocking public acls",
          "Namespace": "builtin.aws.s3.aws0086",
          "Query": "data.builtin.aws.s3.aws0086.deny",
          "Resolution": "Enable blocking any PUT calls with a public ACL specified",
          "Severity": "HIGH",
          "PrimaryURL": "https://avd.aquasec.com/misconfig/aws-0086",
          "References": [
            "https://docs.aws.amazon.com/AmazonS3/latest/dev/access-control-block-public-access.html",
            "https://avd.aquasec.com/misconfig/aws-0086"
          ],
          "Status": "FAIL",
          "CauseMetadata": {
            "Resource": "aws_s3_bucket.b",
            "Provider": "AWS",
            "Service": "s3",
            "StartLine": 1,
            "EndLine": 3
          }
        },
        {
          "Type": "Terraform Security Check",
          "ID": "AWS-0090",
          "Title": "S3 Data should be versioned",
          "Description": "Versioning in Amazon S3 is a means of keeping multiple variants of an object in the same bucket.\n",
          "Message": "Bucket does not have versioning enabled",
          "Namespace": "builtin.aws.s3.aws0090",
          "Query": "data.builtin.aws.s3.aws0090.deny",
          "Resolution": "Enable versioning to protect against accidental/malicious removal or modification",
          "Severity": "MEDIUM",
          "PrimaryURL": "https://avd.aquasec.com/misconfig/aws-0090",
          "References": [
            "https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html",
            "https://avd.aquasec.com/misconfig/aws-0090"
          ],
          "Status": "FAIL",
          "CauseMetadata": {
            "Resource": "aws_s3_bucket.b",
            "Provider": "AWS",
            "Service": "s3",
            "StartLine": 1,
            "EndLine": 3
          }
        },
        {
          "Type": "Terraform Security Check",
          "ID": "AWS-0124",
          "Title": "Missing description for security group rule.",
          "Description": "Security group rules should include a description for auditing purposes.\n",
          "Message": "Security group rule does not have a description.",
          "Namespace": "builtin.aws.ec2.aws0124",
          "Query": "data.builtin.aws.ec2.aws0124.deny",
          "Resolution": "Add descriptions for all security groups rules",
          "Severity": "LOW",
          "PrimaryURL": "https://avd.aquasec.com/misconfig/aws-0124",
          "References": [
            "https://www.cloudconformity.com/knowledge-base/aws/EC2/security-group-rules-description.html",
            "https://avd.aquasec.com/misconfig/aws-0124"
          ],
          "Status": "FAIL",
          "CauseMetadata": {
            "Resource": "aws_security_group_rule.r",
            "Provider": "AWS",
            "Service": "ec2",
            "StartLine": 5,
            "EndLine": 12
          }
        },
        {
          "Type": "Terraform Security Check",
          "ID": "AWS-0107",
          "Title": "Security groups should not allow unrestricted ingress to SSH or RDP from any IP address.",
          "Description": "Security groups provide stateful filtering of ingress and egress network traffic to AWS\nresources.\n",
          "Message": "Security group rule allows unrestricted ingress from any IP address.",
          "Namespace": "builtin.aws.ec2.aws0107",
          "Query": "data.builtin.aws.ec2.aws0107.deny",
          "Resolution": "Set a more restrictive CIDR range",
          "Severity": "CRITICAL",
          "PrimaryURL": "https://avd.aquasec.com/misconfig/aws-0107",
          "References": [
            "https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/security-group-rules-reference.html",
            "https://avd.aquasec.com/misconfig/aws-0107"
          ],
          "Status": "FAIL",
          "CauseMetadata": {
            "Resource": "aws_security_group_rule.r",
            "Provider": "AWS",
            "Service": "ec2",
            "StartLine": 7,
            "EndLine": 7
          }
        }
      ]
    }
  ]
}
"#,
		stderr: b"",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: br#"{
  "SchemaVersion": 2,
  "ArtifactName": ".",
  "ArtifactType": "filesystem",
  "Results": [
    {
      "Target": "main.tf",
      "Class": "config",
      "Type": "terraform",
      "Misconfigurations": [
        {
          "Type": "Terraform Security Check",
          "ID": "AWS-0089",
          "Title": "S3 Bucket Logging",
          "Message": "Bucket has logging disabled",
          "Severity": "LOW",
          "PrimaryURL": "https://avd.aquasec.com/misconfig/aws-0089",
          "Status": "FAIL",
          "CauseMetadata": {
            "Resource": "aws_s3_bucket.b",
            "Provider": "AWS",
            "Service": "s3",
            "StartLine": 1,
            "EndLine": 3
          }
        }
      ]
    }
  ]
}
"#,
		stderr: b"",
		exit: 1,
	},
];
