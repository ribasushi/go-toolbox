package cmn //nolint:revive

import (
	"fmt"

	"golang.org/x/xerrors"
)

type cmnErr struct {
	err   error
	frame xerrors.Frame
}

var _ error = &cmnErr{}
var _ fmt.Formatter = &cmnErr{}
var _ xerrors.Formatter = &cmnErr{}
var _ xerrors.Wrapper = &cmnErr{}

func WrErr(err error) error { //nolint:revive
	if err == nil {
		return nil
	}

	// just wrap all the time, revisit if too expensive
	/*
		if _, isWrapped := err.(interface {
			Unwrap() error
		}); isWrapped {
			return err
		}
	*/

	return &cmnErr{err: err, frame: xerrors.Caller(1)}
}
func (e *cmnErr) Unwrap() error              { return e.err }
func (e *cmnErr) Error() string              { return fmt.Sprint(e) }
func (e *cmnErr) Format(s fmt.State, v rune) { xerrors.FormatError(e, s, v) }
func (e *cmnErr) FormatError(p xerrors.Printer) error {
	if xerr, isXerrFmt := e.err.(xerrors.Formatter); isXerrFmt {
		// do not return() the next-in-chain error:
		// the Format below is sufficient to perform implicit depth-first recursion
		_ = xerr.FormatError(p)
	} else {
		p.Print(e.err.Error())
	}
	if p.Detail() {
		e.frame.Format(p)
	}
	return nil
}
