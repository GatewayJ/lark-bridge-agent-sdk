package comments

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var commentHeading = regexp.MustCompile(`^#{1,6}[\t ]+`)
var commentList = regexp.MustCompile(`^(?:[-+*]|[0-9]+[.)])[\t ]+`)
var commentIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var commentCommand = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_.-]*|[./][^\t ]+)$`)

type commentContainer struct {
	quote  bool
	indent int
}

// StripMarkdown 转换明确的格式结构，并保留代码及存在歧义的文字。
func StripMarkdown(text string) string {
	var out strings.Builder
	var fence byte
	var fenceSize int
	var containers []commentContainer
	for _, line := range strings.SplitAfter(text, "\n") {
		if fence != 0 {
			content := stripCommentContainers(line, containers)
			body := strings.TrimSuffix(strings.TrimSuffix(content, "\n"), "\r")
			trimmed := strings.TrimLeft(body, " ")
			marker, size := commentFence(trimmed)
			if len(body)-len(trimmed) <= 3 && marker == fence && size >= fenceSize && strings.TrimSpace(trimmed[size:]) == "" {
				fence = 0
				continue
			}
			out.WriteString(content)
			continue
		}
		content, outer := readCommentContainers(line)
		trimmed := strings.TrimLeft(content, " ")
		indent := len(content) - len(trimmed)
		marker, size := commentFence(trimmed)
		if indent <= 3 && size >= 3 && (marker != '`' || !strings.Contains(trimmed[size:], "`")) {
			fence, fenceSize, containers = marker, size, outer
			continue
		}
		// 缩进代码和具有命令特征的文字保持原始字符。
		if indent >= 4 || strings.HasPrefix(content, "\t") || isCommentCommand(trimmed) {
			out.WriteString(content)
			continue
		}
		out.WriteString(content[:indent])
		out.WriteString(stripCommentInline(commentHeading.ReplaceAllString(trimmed, "")))
	}
	return out.String()
}

// 记录外层引用和列表；代码内部只移除这些容器前缀。
func readCommentContainers(line string) (string, []commentContainer) {
	var containers []commentContainer
	for {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if indent > 3 {
			break
		}
		if strings.HasPrefix(trimmed, ">") {
			line = strings.TrimPrefix(trimmed[1:], " ")
			containers = append(containers, commentContainer{quote: true})
			continue
		}
		if loc := commentList.FindStringIndex(trimmed); loc != nil {
			line = trimmed[loc[1]:]
			containers = append(containers, commentContainer{indent: indent + loc[1]})
			continue
		}
		break
	}
	return line, containers
}

func stripCommentContainers(line string, containers []commentContainer) string {
	for _, container := range containers {
		if container.quote {
			trimmed := strings.TrimLeft(line, " ")
			if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, ">") {
				break
			}
			line = strings.TrimPrefix(trimmed[1:], " ")
		} else {
			prefix := strings.Repeat(" ", container.indent)
			if !strings.HasPrefix(line, prefix) {
				break
			}
			line = line[len(prefix):]
		}
	}
	return line
}

func isCommentCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	if len(fields) == 1 {
		// 单独的 ASCII 通配符与斜体存在歧义，保留原文。
		return len(fields[0]) > 2 && fields[0][1] != '*' && fields[0][len(fields[0])-2] != '*' &&
			strings.HasPrefix(fields[0], "*") && strings.HasSuffix(fields[0], "*") &&
			commentIdentifier.MatchString(strings.Trim(fields[0], "*"))
	}
	if !commentCommand.MatchString(fields[0]) {
		return false
	}
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") || strings.ContainsAny(field, "/\\=") ||
			(strings.Contains(field, "*") && strings.HasSuffix(field, "*")) {
			return true
		}
	}
	return false
}

func commentFence(text string) (byte, int) {
	if len(text) == 0 || (text[0] != '`' && text[0] != '~') {
		return 0, 0
	}
	n := 1
	for n < len(text) && text[n] == text[0] {
		n++
	}
	return text[0], n
}

func stripCommentInline(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		c := text[i]
		if c == '\\' && i+1 < len(text) {
			out.WriteString(text[i : i+2])
			i += 2
			continue
		}
		if c == '`' {
			_, n := commentFence(text[i:])
			if end := matchingCodeEnd(text, i+n, n); end >= 0 {
				out.WriteString(text[i+n : end])
				i = end + n
				continue
			}
			out.WriteString(text[i : i+n])
			i += n
			continue
		}
		// 引号内可能包含命令参数或文件通配符。
		if c == '\'' || c == '"' {
			if end := strings.IndexByte(text[i+1:], c); end >= 0 {
				end += i + 2
				out.WriteString(text[i:end])
				i = end
				continue
			}
		}
		if c == '*' || c == '_' {
			n := 1
			for i+n < len(text) && text[i+n] == c {
				n++
			}
			if n <= 3 && commentBoundary(text[:i], true) {
				delimiter := text[i : i+n]
				if offset := strings.Index(text[i+n:], delimiter); offset > 0 {
					end := i + n + offset
					inner := text[i+n : end]
					first, _ := utf8.DecodeRuneInString(inner)
					last, _ := utf8.DecodeLastRuneInString(inner)
					identifier := c == '_' && commentIdentifier.MatchString(inner)
					if !identifier && unicode.IsLetter(first) && !unicode.IsSpace(last) &&
						!strings.ContainsAny(inner, "\n\r`") && commentBoundary(text[end+n:], false) {
						out.WriteString(stripCommentInline(inner))
						i = end + n
						continue
					}
				}
			}
			out.WriteString(text[i : i+n])
			i += n
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String()
}

func matchingCodeEnd(text string, start, size int) int {
	for start < len(text) {
		offset := strings.IndexByte(text[start:], '`')
		if offset < 0 {
			return -1
		}
		start += offset
		_, n := commentFence(text[start:])
		if n == size {
			return start
		}
		start += n
	}
	return -1
}

func commentBoundary(text string, before bool) bool {
	if text == "" {
		return true
	}
	var r rune
	if before {
		r, _ = utf8.DecodeLastRuneInString(text)
	} else {
		r, _ = utf8.DecodeRuneInString(text)
	}
	return unicode.IsSpace(r) || strings.ContainsRune("，。；：！？、（）()[]{}.,;:!?", r)
}
