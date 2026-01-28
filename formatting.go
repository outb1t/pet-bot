package main

import (
	"html"
	"regexp"
	"strings"
)

// Minimal HTML formatting for Telegram: keeps code blocks, converts headers
// to bold, supports bold/italic and inline links, and escapes the rest.
func formatHTML(text string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	inCode := false

	hrRe := regexp.MustCompile(`^-{3,}$`)
	linkRe := regexp.MustCompile(`\[(.+?)\]\((https?://[^\s)]+)\)`)
	boldRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
	italicRe := regexp.MustCompile(`\*(.+?)\*`)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("</code></pre>")
				inCode = false
			} else {
				b.WriteString("<pre><code>")
				inCode = true
			}
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}

		if inCode {
			b.WriteString(html.EscapeString(line))
		} else {
			if hrRe.MatchString(trimmed) {
				dashCount := len(trimmed)
				if dashCount < 3 {
					dashCount = 3
				}
				line = "<b>" + strings.Repeat("&mdash;", dashCount) + "</b>"
				b.WriteString(line)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				continue
			}

			if strings.HasPrefix(trimmed, "#") {
				content := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
				line = "<b>" + html.EscapeString(content) + "</b>"
			} else {
				escaped := html.EscapeString(line)
				escaped = linkRe.ReplaceAllStringFunc(escaped, func(s string) string {
					m := linkRe.FindStringSubmatch(s)
					return `<a href="` + html.EscapeString(m[2]) + `">` + html.EscapeString(m[1]) + `</a>`
				})
				escaped = boldRe.ReplaceAllStringFunc(escaped, func(s string) string {
					m := boldRe.FindStringSubmatch(s)
					return "<b>" + html.EscapeString(m[1]) + "</b>"
				})
				escaped = italicRe.ReplaceAllStringFunc(escaped, func(s string) string {
					m := italicRe.FindStringSubmatch(s)
					return "<i>" + html.EscapeString(m[1]) + "</i>"
				})
				line = escaped
			}
			b.WriteString(line)
		}

		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}

	if inCode {
		b.WriteString("</code></pre>")
	}

	return b.String()
}

// Telegram MarkdownV2 does not support headings, so we convert them to bold
// and escape special characters, while preserving fenced code blocks.
func formatMarkdownV2(text string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	inCode := false

	hrRe := regexp.MustCompile(`^-{3,}$`)
	escape := strings.NewReplacer(
		`_`, `\_`,
		`~`, `\~`,
		"`", "\\`",
		`>`, `\>`,
		`#`, `\#`,
		`=`, `\=`,
		`|`, `\|`,
		`{`, `\{`,
		`}`, `\}`,
		`\`, `\\`,
	)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("```")
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				inCode = false
			} else {
				lang := strings.TrimPrefix(trimmed, "```")
				b.WriteString("```")
				b.WriteString(lang)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				inCode = true
			}
			continue
		}

		if inCode {
			b.WriteString(line)
		} else {
			if hrRe.MatchString(trimmed) {
				line = strings.Repeat("-", len(trimmed))
				b.WriteString(line)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				continue
			}

			// Replace markdown-style headers (e.g., "### Title") with bold text.
			if strings.HasPrefix(trimmed, "#") {
				parts := strings.Fields(trimmed)
				if len(parts) > 1 {
					line = "*" + strings.TrimSpace(strings.Join(parts[1:], " ")) + "*"
				}
			}
			line = escape.Replace(line)
			b.WriteString(line)
		}

		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}
