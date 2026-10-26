// Command healthcheck GETs a URL and exits 0 if it answers 2xx, 1 otherwise.
// The API's image is distroless (no shell, no wget), so the container's
// healthcheck runs this:
//
//	/app/healthcheck http://localhost:8080/ready
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	url := "http://localhost:8080/ready"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
	_ = res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		fmt.Fprintln(os.Stderr, "healthcheck:", url, "answered", res.Status)
		os.Exit(1)
	}
}
