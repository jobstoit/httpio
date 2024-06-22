package httpio

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

const (
	DefaultConcurrency = 5
	DefaultChunkSize   = 1024 * 1024 * 5 // 5mb
)

const (
	headerRange = "Range"
)

type remoteFile struct {
	client      *http.Client
	req         *http.Request
	rd          *io.PipeReader
	chunkSize   int
	concurrency int
	size        int
	debug       bool
}

type Option func(*remoteFile) error

func (f *remoteFile) Read(p []byte) (int, error) {
	return f.rd.Read(p)
}

// GetContext get's the requested file concurrently in chunks
func GetContext(ctx context.Context, url string, opts ...Option) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	rd, wr := io.Pipe()
	file := &remoteFile{
		client:      http.DefaultClient,
		req:         req,
		rd:          rd,
		concurrency: DefaultConcurrency,
		chunkSize:   DefaultChunkSize,
	}

	if err := Options(opts...)(file); err != nil {
		return nil, err
	}

	sizeReq, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}
	sizeReq.Header = req.Header.Clone()

	res, err := file.client.Do(sizeReq)
	if err != nil {
		return nil, fmt.Errorf("unable to get content range: %w", err)
	}
	defer res.Body.Close()

	if resLen := res.ContentLength; resLen != 0 {
		file.size = int(resLen)
	} else {
		contentRange := res.Header.Get(headerRange)
		parts := strings.Split(contentRange, "/")

		total := -1
		var err error
		// Checking for whether or not a numbered total exists
		// If one does not exist, we will assume the total to be -1, undefined,
		// and sequentially download each chunk until hitting a 416 error
		totalStr := parts[len(parts)-1]
		if totalStr != "*" {
			total, err = strconv.Atoi(totalStr)
			if err != nil {
				return nil, err
			}
		}

		file.size = total
	}

	cl := make(chan struct{}, file.concurrency)
	sl := make(chan struct{}, 1)
	defer close(sl)

	go file.getChunk(ctx, cl, sl, 0, wr)

	if file.debug {
		log.Printf("fetching '%s' with length: %d", file.req.URL.String(), file.size)
	}

	return file, nil
}

// Get get's the requested file concurrently in chunks
func Get(url string, opts ...Option) (io.Reader, error) {
	return GetContext(context.Background(), url, opts...)
}

func (f *remoteFile) getChunk(ctx context.Context, concurrencyLock chan struct{}, sequenceLock <-chan struct{}, start int, wr *io.PipeWriter) {
	if start == f.size+1 {
		defer close(concurrencyLock)

		select {
		case <-ctx.Done():
			wr.CloseWithError(ctx.Err())
		case <-sequenceLock:
			wr.CloseWithError(io.EOF)
		}

		return
	}

	concurrencyLock <- struct{}{}
	defer func() {
		<-concurrencyLock
	}()

	end := start + f.chunkSize
	if end > f.size {
		end = f.size
	}

	next := make(chan struct{}, 1)
	defer close(next)

	go f.getChunk(ctx, concurrencyLock, next, end+1, wr)

	req := f.req.Clone(ctx)
	req.Header.Add(headerRange, fmt.Sprintf("bytes=%d-%d", start, end))

	// TODO: implement retries
	res, err := f.client.Do(req)
	if err != nil {
		wr.CloseWithError(err)
		return
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		wr.CloseWithError(fmt.Errorf("unexpected statuscode: %d: %s", res.StatusCode, res.Status))
		return
	}

	select {
	case <-ctx.Done():
		wr.CloseWithError(ctx.Err())
	case <-sequenceLock:
		_, err = io.Copy(wr, res.Body)
		if err != nil {
			wr.CloseWithError(err)
		}

		if f.debug {
			log.Printf("write '%s', range %d-%d/%d", f.req.URL.String(), start, end, f.size)
		}
	}
}

// Options is a collection of options
func Options(opts ...Option) Option {
	return func(f *remoteFile) error {
		for _, opt := range opts {
			if err := opt(f); err != nil {
				return err
			}
		}

		return nil
	}
}

// WithHeader sets headers to be used with the request
func WithHeader(key, value string) Option {
	return func(f *remoteFile) error {
		f.req.Header.Add(key, value)

		return nil
	}
}

// WithClient sets the client that should be used
func WithClient(client *http.Client) Option {
	return func(f *remoteFile) error {
		if client != nil {
			f.client = client
		}

		return nil
	}
}

// WithConcurrency sets the concurrency limit for this request
func WithConcurrency(c int) Option {
	return func(f *remoteFile) error {
		if c < 1 {
			c = 1
		}

		f.concurrency = c

		return nil
	}
}

// WithChuckSize sets the chunksize for the requests
func WithChunkSize(c int) Option {
	return func(f *remoteFile) error {
		if c < 1 {
			c = DefaultChunkSize
		}

		f.chunkSize = c

		return nil
	}
}

// WithDebug sets the debug flag for debug logs
func WithDebug() Option {
	return func(f *remoteFile) error {
		f.debug = true

		return nil
	}
}
