package parser

import (
	"io"
)

// Handler of HtmlParser results.
type HandlerFunc[Result any] func(val Result)

// Generic Parser.
type Parser[Result any] interface {
	Parse(payload io.Reader, handler HandlerFunc[Result]) error
}

// Adapter to allow a use of functions as Generic Parser.
type Func[Result any] func(payload io.Reader, handler HandlerFunc[Result]) error

// Implements of Generic Parser interface.
func (fnc Func[Result]) Parse(payload io.Reader, handler HandlerFunc[Result]) error {
	return fnc(payload, handler)
}
