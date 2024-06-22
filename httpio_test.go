package httpio_test

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jobstoit/httpio"
)

//go:embed testdata/*
var testdata embed.FS

func TestGet(t *testing.T) {
	testGet(t, "get github logo", "GitHub_logo.png")
	testGet(t, "get 5mb", "test_5mb")
	testGet(t, "get 12mb", "test_12mb")
	testGet(t, "get 32mb", "test_32mb")
}

func testGet(t *testing.T, name string, file string) {
	t.Run(name, func(t *testing.T) {
		// t.Parallel()
		svr := newTestServer()
		defer svr.Close()
		u := svr.URL()

		expectedFile, err := testdata.Open(fmt.Sprintf("testdata/%s", file))
		if err != nil {
			t.Fatalf("cannot find file in testdata: %v", err)
		}

		expectHash := sha256.New()
		if _, err := io.Copy(expectHash, expectedFile); err != nil {
			t.Fatalf("unexpected error reading bindata: %v", err)
		}

		u = u.JoinPath("assets", file)
		remoteFile, err := httpio.Get(u.String())
		if err != nil {
			t.Fatalf("failed to setup request: %v", err)
		}

		hash := sha256.New()
		if _, err := io.Copy(hash, remoteFile); err != nil {
			t.Errorf("unable to write file: %v", err)
		}

		if e, a := fmt.Sprintf("%x", expectHash.Sum(nil)), fmt.Sprintf("%x", hash.Sum(nil)); e != a {
			t.Errorf("mismatched hashes, expected: '%s', but got '%s'", e, a)
		}
	})
}

type testServer struct {
	server *httptest.Server
}

func newTestServer() *testServer {
	ts := &testServer{}

	assets, _ := fs.Sub(testdata, "testdata")

	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))

	ts.server = httptest.NewServer(mux)

	return ts
}

func (s *testServer) Close() {
	s.server.Close()
}

func (s *testServer) URL() *url.URL {
	u, _ := url.Parse(s.server.URL)

	return u
}

// func (s *testServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
// 	w.Header().Add(httpio.HeaderLength, fmt.Sprintf("%d", s.size))
// 	w.Header().Add("Content-Type", "text/plain")
//
// 	delta := s.size
// 	if ran := req.Header.Get(httpio.HeaderRange); ran != "" {
// 		w.Header().Add(httpio.HeaderRange, ran)
//
// 		if ran == "bytes=0-0" {
// 			w.WriteHeader(http.StatusPartialContent)
// 			return
// 		}
//
// 		fromtoBytes := strings.Split(strings.TrimPrefix(ran, "bytes="), "-")
// 		start, err := strconv.ParseInt(fromtoBytes[0], 10, 64)
// 		if err != nil {
// 			w.WriteHeader(http.StatusInternalServerError)
// 			return
// 		}
// 		end, err := strconv.ParseInt(fromtoBytes[1], 10, 64)
// 		if err != nil {
// 			w.WriteHeader(http.StatusInternalServerError)
// 			return
// 		}
//
// 		delta = end - start
// 	}
//
// 	w.WriteHeader(http.StatusPartialContent)
// 	_, err := io.Copy(w, io.LimitReader(s.file, delta))
// 	if err != nil {
// 		log.Printf("error writing: %v", err)
// 	}
// }
