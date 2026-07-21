// caddy-admin-client 是发行门禁专用的最小 AF_UNIX HTTP 客户端。
// 它使用与主程序相同的 Go net/http + Unix socket 语义，不监听 TCP。
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	socketPath := flag.String("socket", "", "AF_UNIX socket filesystem path")
	method := flag.String("method", http.MethodGet, "HTTP method")
	requestPath := flag.String("path", "/config/", "Caddy admin path")
	bodyFile := flag.String("body-file", "", "optional request body file")
	flag.Parse()
	if *socketPath == "" || *requestPath == "" || (*requestPath)[0] != '/' {
		fmt.Fprintln(os.Stderr, "--socket and an absolute --path are required")
		os.Exit(2)
	}
	var body []byte
	var err error
	if *bodyFile != "" {
		body, err = os.ReadFile(*bodyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", *socketPath)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	request, err := http.NewRequest(*method, "http://unix"+*requestPath, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "%s: %s\n", response.Status, responseBody)
		os.Exit(1)
	}
	fmt.Println(response.Status)
}
