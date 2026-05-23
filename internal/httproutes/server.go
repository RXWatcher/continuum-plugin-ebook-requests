// Package httproutes adapts a stdlib http.Handler to the SDK's HttpRoutes.v1
// gRPC service.
package httproutes

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// maxBodyBytes caps the request body the host may hand us. This is the JSON
// API surface (search/external_search); file transfers are GET. The body is
// fully buffered, so an unbounded one is a memory-exhaustion vector.
const maxBodyBytes = 8 << 20 // 8 MiB

type Server struct {
	pluginv1.UnimplementedHttpRoutesServer
	handler atomic.Pointer[http.Handler]
}

func NewServer() *Server { return &Server{} }

func (s *Server) SetHandler(h http.Handler) {
	if h == nil {
		s.handler.Store(nil)
		return
	}
	s.handler.Store(&h)
}

// isHTTPToken reports whether s is a valid RFC7230 method token. httptest /
// http.ReadRequest panic or error on a method with spaces/control chars, and
// method comes straight from the (untrusted) RPC payload.
func isHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e || strings.ContainsRune("()<>@,;:\\\"/[]?={} \t", r) {
			return false
		}
	}
	return true
}

func errResponse(code int32, msg string) *pluginv1.HandleHTTPResponse {
	return &pluginv1.HandleHTTPResponse{
		StatusCode: code,
		Body:       []byte(`{"error":{"message":"` + msg + `"}}`),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
}

func (s *Server) Handle(ctx context.Context, req *pluginv1.HandleHTTPRequest) (resp *pluginv1.HandleHTTPResponse, _ error) {
	// Defense in depth: a panic anywhere in request reconstruction or the
	// downstream handler must not take down the gRPC serving goroutine.
	defer func() {
		if rec := recover(); rec != nil {
			resp = errResponse(http.StatusInternalServerError, "internal error")
		}
	}()

	hPtr := s.handler.Load()
	if hPtr == nil {
		return &pluginv1.HandleHTTPResponse{
			StatusCode: http.StatusServiceUnavailable,
			Body:       []byte(`{"error":{"code":"not_ready","message":"plugin not configured"}}`),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}
	h := *hPtr

	if b := req.GetBody(); len(b) > maxBodyBytes {
		return errResponse(http.StatusRequestEntityTooLarge, "request body too large"), nil
	}

	rawQuery := ""
	if req.GetQuery() != nil {
		vals := url.Values{}
		for k, v := range req.GetQuery().GetFields() {
			// Use the scalar value, not v.String() (which is the protobuf
			// debug form: a number arrives as "number_value:50", corrupting
			// ?limit= / ?order= so pagination/sort silently break).
			switch val := v.AsInterface().(type) {
			case string:
				vals.Set(k, val)
			case bool:
				vals.Set(k, strconv.FormatBool(val))
			case float64:
				vals.Set(k, strconv.FormatFloat(val, 'f', -1, 64))
			}
		}
		rawQuery = vals.Encode()
	}

	method := req.GetMethod()
	if method == "" {
		method = http.MethodGet
	}
	if !isHTTPToken(method) {
		return errResponse(http.StatusBadRequest, "invalid method"), nil
	}

	u := &url.URL{Path: req.GetPath(), RawQuery: rawQuery}
	// http.NewRequestWithContext returns an error (rather than panicking like
	// httptest.NewRequest) on an unparseable method/URL.
	httpReq, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(req.GetBody()))
	if err != nil {
		return errResponse(http.StatusBadRequest, "invalid request"), nil
	}
	httpReq.RequestURI = u.RequestURI()
	for k, v := range req.GetHeaders() {
		httpReq.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httpReq)

	res := rec.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	headers := map[string]string{}
	for k, vs := range res.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}
	return &pluginv1.HandleHTTPResponse{
		StatusCode: int32(rec.Code),
		Headers:    headers,
		Body:       body,
	}, nil
}
