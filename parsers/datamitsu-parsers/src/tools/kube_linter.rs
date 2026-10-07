//! kube_linter — KubeLinter static analysis of Kubernetes YAML / Helm charts.
//! Ported from the none-ls diagnostics/kube_linter builtin.
//!
//! kube-linter emits `{"Reports":[ … ]}` where each report nests its message,
//! remediation, check id, and file path. The builtin's custom `on_output`
//! concatenates `Diagnostic.Message` + "\n" + `Remediation` and takes `Check` as
//! the code. A report carries no level, so no finding has one, and there is no
//! row/column in the output.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "kube_linter",
	description: "KubeLinter is a static analysis tool that checks Kubernetes YAML files and Helm charts to ensure the applications represented in them adhere to best practices.",
	url: "https://github.com/stackrox/kube-linter",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	// Upstream runs against $ROOT (a directory of manifests), not stdin.
	operations: &[Operation {
		mode: "lint",
		args: &["lint", "--format", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let reports = match &value {
		JsonValue::Object(m) => match m.get("Reports") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};
	reports.iter().filter_map(report_to_diag).collect()
}

fn report_to_diag(report: &JsonValue) -> Option<RawDiagnostic> {
	let map = match report {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	// message = Diagnostic.Message .. "\n" .. Remediation
	let diag_message = map
		.get("Diagnostic")
		.and_then(as_object)
		.and_then(|d| get_str(d, "Message"))
		.unwrap_or_default();
	let remediation = get_str(map, "Remediation").unwrap_or_default();
	let message = format!("{diag_message}\n{remediation}");

	let code = get_str(map, "Check");
	let file = map
		.get("Object")
		.and_then(as_object)
		.and_then(|o| o.get("Metadata"))
		.and_then(as_object)
		.and_then(|m| get_str(m, "FilePath"))
		.as_deref()
		.and_then(crate::diagnostic::file_field);

	Some(RawDiagnostic {
		message,
		source: Some("kube-linter".to_string()),
		code,
		file,
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

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_report() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(
			out[0].message,
			"container \"app\" does not have a CPU request\nSet the CPU request for your container."
		);
		assert_eq!(out[0].code.as_deref(), Some("unset-cpu-requirements"));
		assert_eq!(out[0].source.as_deref(), Some("kube-linter"));
		assert_eq!(out[0].row, None);
		assert_eq!(out[0].col, None);
	}

	#[test]
	fn never_sets_a_severity() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert!(out.iter().all(|d| d.severity.is_none()), "{out:?}");
	}

	#[test]
	fn empty_reports_yield_nothing() {
		assert!(parse(br#"{"Reports":[]}"#, b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}
	#[test]
	fn names_the_file_of_each_report() {
		let json = br#"{"Reports":[{"Diagnostic":{"Message":"m"},"Check":"c","Remediation":"r","Object":{"Metadata":{"FilePath":"deploy/app.yaml"}}}]}"#;
		assert_eq!(parse(json, b"", 1)[0].file.as_deref(), Some("deploy/app.yaml"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"{
    "Reports": [
        {
            "Check": "unset-cpu-requirements",
            "Diagnostic": { "Message": "container \"app\" does not have a CPU request" },
            "Remediation": "Set the CPU request for your container.",
            "Object": { "Metadata": { "FilePath": "deploy.yaml" } }
        }
    ]
}"#,
	stderr: b"",
	exit: 1,
}];
