//! A tokenizer for the two flat XML shapes the format parsers read (Checkstyle
//! and JUnit): start tags with attributes, end tags, self-closing tags, text,
//! CDATA, the five predefined entities and numeric character references.
//!
//! Written by hand so the crate keeps `tinyjson` as its only dependency. It
//! knows no namespaces (a prefixed name stays one name) and no DTD: a
//! declaration, a processing instruction and a comment are skipped whole. Input
//! it cannot read — a `<` whose tag never closes — ends the token stream there,
//! so a truncated document yields the tokens before the cut.

/// One piece of a document.
#[derive(Debug, PartialEq, Eq)]
pub(crate) enum Token<'a> {
	/// `<name attr="v">`, or `<name/>` with `self_closing`.
	Start {
		name: &'a str,
		attrs: Vec<(&'a str, String)>,
		self_closing: bool,
	},
	/// `</name>`.
	End { name: &'a str },
	/// Character data between tags, entities decoded; the content of a CDATA
	/// section verbatim.
	Text(String),
}

/// The tokens of `src`, in order.
pub(crate) struct Tokenizer<'a> {
	src: &'a str,
	pos: usize,
}

impl<'a> Tokenizer<'a> {
	pub(crate) fn new(src: &'a str) -> Self {
		Tokenizer { src, pos: 0 }
	}

	/// Skip to the end of `close` after the current position; false when it
	/// never comes.
	fn skip_past(&mut self, close: &str) -> bool {
		match self.src[self.pos..].find(close) {
			Some(i) => {
				self.pos += i + close.len();
				true
			}
			None => {
				self.pos = self.src.len();
				false
			}
		}
	}

	fn tag(&mut self) -> Option<Token<'a>> {
		let rest = &self.src[self.pos..];
		let end = tag_end(rest)?;
		let inner = &rest[1..end];
		self.pos += end + 1;
		if let Some(name) = inner.strip_prefix('/') {
			return Some(Token::End { name: name.trim() });
		}
		let (inner, self_closing) = match inner.strip_suffix('/') {
			Some(inner) => (inner, true),
			None => (inner, false),
		};
		let name_end = inner.find(|c: char| c.is_ascii_whitespace()).unwrap_or(inner.len());
		Some(Token::Start {
			name: &inner[..name_end],
			attrs: attributes(&inner[name_end..]),
			self_closing,
		})
	}
}

impl<'a> Iterator for Tokenizer<'a> {
	type Item = Token<'a>;

	fn next(&mut self) -> Option<Token<'a>> {
		loop {
			let rest = &self.src[self.pos..];
			if rest.is_empty() {
				return None;
			}
			if !rest.starts_with('<') {
				let end = rest.find('<').unwrap_or(rest.len());
				self.pos += end;
				return Some(Token::Text(decode(&rest[..end])));
			}
			if let Some(body) = rest.strip_prefix("<![CDATA[") {
				let end = body.find("]]>")?;
				self.pos += "<![CDATA[".len() + end + "]]>".len();
				return Some(Token::Text(body[..end].to_string()));
			}
			let skipped = if rest.starts_with("<!--") {
				self.skip_past("-->")
			} else if rest.starts_with("<?") {
				self.skip_past("?>")
			} else if rest.starts_with("<!") {
				self.skip_past(">")
			} else {
				return self.tag();
			};
			if !skipped {
				return None;
			}
		}
	}
}

/// The index of the `>` that closes the tag `rest` starts with, outside any
/// quoted attribute value.
fn tag_end(rest: &str) -> Option<usize> {
	let mut quote: Option<char> = None;
	for (i, c) in rest.char_indices().skip(1) {
		match quote {
			Some(q) if c == q => quote = None,
			Some(_) => {}
			None if c == '"' || c == '\'' => quote = Some(c),
			None if c == '>' => return Some(i),
			None if c == '<' => return None,
			None => {}
		}
	}
	None
}

/// The `name="value"` pairs of a start tag, values decoded. A name without a
/// value is skipped.
fn attributes(s: &str) -> Vec<(&str, String)> {
	let mut out = Vec::new();
	let mut rest = s.trim_start();
	while !rest.is_empty() {
		let Some(eq) = rest.find('=') else {
			break;
		};
		let name = rest[..eq].trim();
		let after = rest[eq + 1..].trim_start();
		let Some(quote) = after.chars().next().filter(|c| *c == '"' || *c == '\'') else {
			break;
		};
		let Some(close) = after[1..].find(quote) else {
			break;
		};
		if !name.is_empty() && !name.contains(char::is_whitespace) {
			out.push((name, decode(&after[1..1 + close])));
		}
		rest = after[1 + close + 1..].trim_start();
	}
	out
}

/// `s` with its entity and character references replaced. A reference it does
/// not know stays as written.
pub(crate) fn decode(s: &str) -> String {
	let mut out = String::with_capacity(s.len());
	let mut rest = s;
	while let Some(amp) = rest.find('&') {
		out.push_str(&rest[..amp]);
		let after = &rest[amp..];
		match after
			.find(';')
			.and_then(|semi| Some((reference(&after[1..semi])?, semi)))
		{
			Some((c, semi)) => {
				out.push(c);
				rest = &after[semi + 1..];
			}
			None => {
				out.push('&');
				rest = &after[1..];
			}
		}
	}
	out.push_str(rest);
	out
}

fn reference(name: &str) -> Option<char> {
	match name {
		"lt" => Some('<'),
		"gt" => Some('>'),
		"amp" => Some('&'),
		"quot" => Some('"'),
		"apos" => Some('\''),
		_ => {
			let digits = name.strip_prefix('#')?;
			let code = match digits.strip_prefix(['x', 'X']) {
				Some(hex) => u32::from_str_radix(hex, 16).ok()?,
				None => digits.parse().ok()?,
			};
			char::from_u32(code)
		}
	}
}

/// The places in `text` where a document whose root is `root` may begin: the
/// start tag opening a line (after indentation) or following an XML
/// declaration. A tag in the middle of other text is quoted, not a document.
pub(crate) fn roots<'a>(text: &'a str, root: &'a str) -> impl Iterator<Item = &'a str> + 'a {
	let open = format!("<{root}");
	text
		.match_indices(&open)
		.map(|(i, _)| i)
		.filter(move |&i| {
			let line_start = text[..i].rfind('\n').map_or(0, |n| n + 1);
			let before = text[line_start..i].trim();
			before.is_empty() || before.ends_with("?>")
		})
		.map(move |i| &text[i..])
		.collect::<Vec<_>>()
		.into_iter()
}

/// The value of attribute `name`, if the tag has one.
pub(crate) fn attr<'t>(attrs: &'t [(&str, String)], name: &str) -> Option<&'t str> {
	attrs.iter().find(|(k, _)| *k == name).map(|(_, v)| v.as_str())
}

#[cfg(test)]
mod tests {
	use super::*;

	fn tokens(s: &str) -> Vec<Token<'_>> {
		Tokenizer::new(s).collect()
	}

	#[test]
	fn reads_tags_attributes_and_text() {
		let t = tokens(r#"<?xml version="1.0"?><a x="1" y='two'><b/>hi</a>"#);
		assert_eq!(
			t,
			vec![
				Token::Start {
					name: "a",
					attrs: vec![("x", "1".to_string()), ("y", "two".to_string())],
					self_closing: false
				},
				Token::Start {
					name: "b",
					attrs: vec![],
					self_closing: true
				},
				Token::Text("hi".to_string()),
				Token::End { name: "a" },
			]
		);
	}

	#[test]
	fn decodes_the_predefined_entities_and_character_references() {
		assert_eq!(decode("&lt;a&gt; &amp; &quot;b&quot; &apos;c&apos;"), "<a> & \"b\" 'c'");
		assert_eq!(decode("&#65;&#x42;&#X43;"), "ABC");
		assert_eq!(decode("a & b &nbsp; &#xZZ; &"), "a & b &nbsp; &#xZZ; &");
	}

	#[test]
	fn keeps_a_quoted_greater_than_inside_an_attribute() {
		let t = tokens(r#"<error message="a > b &amp; c" source='x"y'/>"#);
		assert_eq!(
			t,
			vec![Token::Start {
				name: "error",
				attrs: vec![("message", "a > b & c".to_string()), ("source", "x\"y".to_string())],
				self_closing: true
			}]
		);
	}

	#[test]
	fn reads_cdata_verbatim_and_skips_comments_and_doctypes() {
		let t = tokens("<!DOCTYPE x><!-- <no/> --><f><![CDATA[a <b> &amp;]]></f>");
		assert_eq!(
			t,
			vec![
				Token::Start {
					name: "f",
					attrs: vec![],
					self_closing: false
				},
				Token::Text("a <b> &amp;".to_string()),
				Token::End { name: "f" },
			]
		);
	}

	#[test]
	fn a_truncated_document_yields_the_tokens_before_the_cut() {
		let t = tokens(r#"<checkstyle><file name="a"><error line="1"/><error line="2" mess"#);
		assert_eq!(t.len(), 3);
		assert!(tokens("<!-- never closed").is_empty());
		assert!(tokens("<![CDATA[never closed").is_empty());
	}

	#[test]
	fn a_prefixed_name_stays_one_name() {
		let t = tokens("<ns:a/>");
		assert!(matches!(&t[0], Token::Start { name: "ns:a", .. }));
	}
}
