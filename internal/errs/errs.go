// Package errs 定义合并引擎对外暴露的错误分类。
//
// 底层第三方库的任何异常都不会直接透出，而是被归一化为
// 本包中带分类码（Code）的错误，并保留原始上下文（Cause）。
package errs

import (
	"errors"
	"fmt"
)

// Code 是归一化后的错误分类码。
type Code string

const (
	// CodeInvalidInput 输入格式不完整 / 缺少必要字段 / 行区间非法。
	CodeInvalidInput Code = "invalid_input"
	// CodeParseSyntax 底层差异解析库报告的语法错误。
	CodeParseSyntax Code = "parse_syntax"
	// CodeParseLibrary 底层差异解析库内部异常（panic 等）。
	CodeParseLibrary Code = "parse_library"
	// CodeNotFound 本地存储中不存在指定状态。
	CodeNotFound Code = "not_found"
	// CodeConflict 合并发生语义冲突（正常结论而非引擎故障）。
	CodeConflict Code = "conflict"
)

// EngineError 是引擎对外暴露的唯一具体错误类型。
type EngineError struct {
	Code    Code
	Message string
	Cause   error // 保留的原始上下文（可能为库内部错误）
}

func (e *EngineError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *EngineError) Unwrap() error { return e.Cause }

// New 构造一个指定分类的引擎错误。
func New(code Code, message string, cause error) *EngineError {
	return &EngineError{Code: code, Message: message, Cause: cause}
}

// Wrapf 在既有错误上补充上下文并保持分类码。
func Wrapf(code Code, cause error, format string, args ...any) *EngineError {
	return &EngineError{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// AsCode 尝试从错误中提取分类码；失败返回空串。
func AsCode(err error) Code {
	if err == nil {
		return ""
	}
	var ee *EngineError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ""
}
