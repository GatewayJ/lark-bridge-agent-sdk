package comments

import (
	"strings"
	"testing"
)

func TestStripMarkdownPreservesCodeAndPlainText(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"identifiers", "foo_bar_baz __init__ _private", "foo_bar_baz __init__ _private"},
		{"operators", "a * b\n*.go\nfoo*bar*baz", "a * b\n*.go\nfoo*bar*baz"},
		{"command", "rg --glob '*.go' --glob '*.txt' foo_bar_baz /tmp/a_b > out.txt", "rg --glob '*.go' --glob '*.txt' foo_bar_baz /tmp/a_b > out.txt"},
		{"inline", "执行 `a * b + __init__`。", "执行 a * b + __init__。"},
		{"backticks", "``a ` b``", "a ` b"},
		{"fenced", "```go\n    foo_bar_baz := a * b\n\n    // **literal**\n```", "    foo_bar_baz := a * b\n\n    // **literal**\n"},
		{"long fence", "````c++\n```\n__init__\n````\n后续", "```\n__init__\n后续"},
		{"tilde fence", "~~~sh\n*.go\n~~~", "*.go\n"},
		{"indent", "    __init__\n    # comment\n\n    a * b", "    __init__\n    # comment\n\n    a * b"},
		{"markdown", "# 标题\n\n- **完成**\n- *说明*\n\n> 引用\n1. 下一步", "标题\n\n完成\n说明\n\n引用\n下一步"},
		{"punctuation", "**bold**, *italic*!", "bold, italic!"},
		{"CRLF code", "```sh\r\n    echo *.go\r\n```\r\n", "    echo *.go\r\n"},
		{"emphasis", "__中文说明__ **bold** _普通说明_", "中文说明 bold 普通说明"},
		{"unmatched", "a ` foo_bar_baz\n**unfinished", "a ` foo_bar_baz\n**unfinished"},
		{"escaped", "\\*literal\\*", "\\*literal\\*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripMarkdown(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFinalizeReplyPreservesIndentAndLengthLimit(t *testing.T) {
	input := "    foo_bar_baz := a * b\n\n    __init__()\n"
	if got := finalizeReply(input, ""); got != input {
		t.Fatalf("got %q, want %q", got, input)
	}
	if got := finalizeReply(" \n\t", ""); got != "（无回复内容）" {
		t.Fatalf("empty reply: %q", got)
	}
}

func TestFinalizeReplyRetainsCommentLimit(t *testing.T) {
	input := strings.Repeat("文", ReplyMaxChars+1)
	want := strings.Repeat("文", ReplyMaxChars-1) + "…"
	if got := finalizeReply(input, ""); got != want {
		t.Fatalf("unexpected comment truncation: %d", len([]rune(got)))
	}
}

func TestStripMarkdownProtectsCommandsAndNestedCode(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"find glob", "find . -name *test*", "find . -name *test*"},
		{"ls glob", "ls *test*", "ls *test*"},
		{"bold", "**bold**", "bold"},
		{"bare glob", "*test*", "*test*"},
		{"command in list", "- find . -name *test*", "find . -name *test*"},
		{"quoted code", "> ```python\n> # comment\n> value = \"**literal**\"\n> ```", "# comment\nvalue = \"**literal**\"\n"},
		{"quoted indentation", "> ```python\n>     __init__()\n> \n>     a * b\n> ```\n后续", "    __init__()\n\n    a * b\n后续"},
		{"list code", "- ```sh\n  # comment\n  find . -name *test*\n  ```", "# comment\nfind . -name *test*\n"},
		{"quote list code", "> - ```sh\n>   # comment\n>   > literal\n>   ```", "# comment\n> literal\n"},
		{"list quote code", "- > ```sh\n  > # comment\n  > ```", "# comment\n"},
		{"nested quotes", "> > ```\n> > - literal\n> > ```", "- literal\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripMarkdown(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
