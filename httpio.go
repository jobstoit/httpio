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
	HeaderRange  = "Range"
	HeaderLength = "Content-Length"
)

type RemoteFile struct {
	client      *http.Client
	req         *http.Request
	rd          *io.PipeReader
	chunkSize   int
	concurrency int
	size        int
}

type RemoteFileOption func(*RemoteFile) error

func (f *RemoteFile) Read(p []byte) (int, error) {
	return f.rd.Read(p)
}

func GetContext(ctx context.Context, url string, opts ...RemoteFileOption) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	rd, wr := io.Pipe()
	file := &RemoteFile{
		client:      http.DefaultClient,
		req:         req,
		rd:          rd,
		concurrency: DefaultConcurrency,
		chunkSize:   DefaultChunkSize,
	}

	if err := RemoteFileOptions(opts...)(file); err != nil {
		return nil, err
	}

	sizeReq, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}

	res, err := file.client.Do(sizeReq)
	if err != nil {
		return nil, fmt.Errorf("unable to get content range: %w", err)
	}
	defer res.Body.Close()

	if resLen := res.Header.Get(HeaderLength); resLen != "" {
		scl, err := strconv.Atoi(resLen)
		if err != nil {
			return nil, fmt.Errorf("unable to parse content len: %w", err)
		}

		file.size = scl
	} else {
		contentRange := res.Header.Get(HeaderRange)
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

	return file, nil
}

func Get(url string, opts ...RemoteFileOption) (io.Reader, error) {
	return GetContext(context.Background(), url, opts...)
}

func (f *RemoteFile) getChunk(ctx context.Context, concurrencyLock chan struct{}, sequenceLock <-chan struct{}, start int, wr *io.PipeWriter) {
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
	req.Header.Add(HeaderRange, fmt.Sprintf("bytes=%d-%d", start, end))

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
		log.Printf("write '%s', range %d-%d/%d", f.req.URL.String(), start, end, f.size)
	}
}

func RemoteFileOptions(opts ...RemoteFileOption) RemoteFileOption {
	return func(f *RemoteFile) error {
		for _, opt := range opts {
			if err := opt(f); err != nil {
				return err
			}
		}

		return nil
	}
}

func WithHeader(key, value string) RemoteFileOption {
	return func(f *RemoteFile) error {
		f.req.Header.Add(key, value)

		return nil
	}
}

func WithClient(client *http.Client) RemoteFileOption {
	return func(f *RemoteFile) error {
		if client != nil {
			f.client = client
		}

		return nil
	}
}
