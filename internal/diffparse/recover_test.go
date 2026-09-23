package diffparse

import (
	"context"
	"strings"
	"testing"

	"github.com/lisama569/merge-engine-go/internal/errs"
	"github.com/lisama569/merge-engine-go/internal/model"
	dmp "github.com/sergi/go-diff/diffmatchpatch"
)

// TestNormalizeLibraryPanicMapsToLibraryError 注入"第三方库内部 panic"：
// 解析器边界必须 recover 并归一化为 parse_library，保留原始上下文。
func TestNormalizeLibraryPanicMapsToLibraryError(t *testing.T) {
	faulty := func(body string) ([]dmp.Patch, error) {
		panic("runtime error: invalid memory address in third-party diff")
	}
	p := newGoDiffParserWithFault(faulty)

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic must be normalized inside parser, leaked: %v", r)
			}
		}()
		_, _, err = p.Normalize(context.Background(), model.RawChangeSet{
			Author: "x",
			TextDiffs: []model.RawTextDiff{
				{Path: "f", Body: "@@ -1,1 +1,1 @@\n-a\n+b"},
			},
		})
	}()
	if err == nil {
		t.Fatal("injected library panic must produce an error")
	}
	code := errs.AsCode(err)
	t.Logf("normalized error: %v (code=%s)", err, code)
	if code != errs.CodeParseLibrary {
		t.Fatalf("library panic must map to parse_library, got %s", code)
	}
	if !strings.Contains(err.Error(), "invalid memory address") {
		t.Fatalf("original panic context must be preserved: %v", err)
	}
}

// TestNormalizeLibrarySyntaxDistinctFromPanic 语法错误与内部异常必须可区分。
func TestNormalizeLibrarySyntaxDistinctFromPanic(t *testing.T) {
	faulty := func(body string) ([]dmp.Patch, error) {
		return nil, &syntaxLikeError{msg: "bad hunk syntax at token @@"}
	}
	p := newGoDiffParserWithFault(faulty)
	_, _, err := p.Normalize(context.Background(), model.RawChangeSet{
		Author:    "x",
		TextDiffs: []model.RawTextDiff{{Path: "f", Body: "@@ -1,1 +1,1 @@\n-a\n+b"}},
	})
	if errs.AsCode(err) != errs.CodeParseSyntax {
		t.Fatalf("library-reported syntax failure must map to parse_syntax, got %v", err)
	}
}

type syntaxLikeError struct{ msg string }

func (e *syntaxLikeError) Error() string { return e.msg }
