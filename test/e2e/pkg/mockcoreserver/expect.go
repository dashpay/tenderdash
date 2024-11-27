package mockcoreserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"github.com/dashpay/dashd-go/btcjson"
)

// BodyShouldBeSame compares a body from received request and passed byte slice
func BodyShouldBeSame(v interface{}) ExpectFunc {
	var body []byte
	switch t := v.(type) {
	case []byte:
		body = t
	case string:
		body = []byte(t)
	default:
		log.Panicf("unsupported type %q", t)
	}
	return func(req *http.Request) error {
		buf, err := io.ReadAll(req.Body)
		if err != nil {
			return err
		}
		err = req.Body.Close()
		if err != nil {
			return err
		}
		req.Body = io.NopCloser(bytes.NewBuffer(buf))
		if !bytes.Equal(body, buf) {
			return fmt.Errorf(
				"the request body retried by URL %s is not equal\nexpected: %s\nactual: %s",
				req.URL.String(),
				buf,
				body,
			)
		}
		return nil
	}
}

// BodyShouldBeEmpty expects that a request body should be empty
func BodyShouldBeEmpty() ExpectFunc {
	return BodyShouldBeSame("")
}

// QueryShouldHave expects that a request query values should match on passed values
func QueryShouldHave(expectedVales url.Values) ExpectFunc {
	return func(req *http.Request) error {
		actuallyVales := req.URL.Query()
		for k, eVals := range expectedVales {
			aVals, ok := actuallyVales[k]
			if !ok {
				return fmt.Errorf("query parameter %q not found in a request", k)
			}
			for i, ev := range eVals {
				if aVals[i] != ev {
					return fmt.Errorf("query parameter %q should be equal to %q", aVals[i], ev)
				}
			}
		}
		return nil
	}
}

// And ...
func And(fns ...ExpectFunc) ExpectFunc {
	return func(req *http.Request) error {
		for _, fn := range fns {
			err := fn(req)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// JRPCRequest transforms http.Request into btcjson.Request and executes passed list of functions
func JRPCRequest(fns ...func(ctx context.Context, req btcjson.Request) error) ExpectFunc {
	return func(req *http.Request) error {
		jReq, ok := req.Context().Value(jRPCRequestKey{}).(btcjson.Request)
		if !ok {
			return errors.New("missed btcjson.Request in a context")
		}
		for _, fn := range fns {
			err := fn(req.Context(), jReq)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// JRPCParamsEmpty is a request expectation of empty JRPC params
func JRPCParamsEmpty() ExpectFunc {
	return JRPCRequest(func(_ context.Context, req btcjson.Request) error {
		if len(req.Params) > 0 {
			return errors.New("jRPC request params should be empty")
		}
		return nil
	})
}

// Debug is a debug JRPC request handler
func Debug() ExpectFunc {
	return func(req *http.Request) error {
		buf, err := io.ReadAll(req.Body)
		if err != nil {
			return err
		}
		req.Body = io.NopCloser(bytes.NewBuffer(buf))
		return nil
	}
}
