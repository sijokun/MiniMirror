package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
)

type replaceItem struct {
	Old string `json:"old"`
	New string `json:"new"`
}

var (
	TargetDomain       = os.Getenv("TARGET_DOMAIN")
	TargetEndpoint     = os.Getenv("TARGET_ENDPOINT")
	SecondaryDomains   = strings.Split(os.Getenv("SECONDARY_DOMAINS"), ";")
	port               = os.Getenv("PORT")
	ReplaceItemsString = os.Getenv("REPLACE")
	ReplaceItems       []replaceItem
)

var errorLog = log.New(os.Stderr, "", 0)

const MaxRetry = 3

func mirrorUrl(url string, c *fiber.Ctx, retry int8) error {
	log.Printf("mirroring %s", url)
	reqBodyBuffer := bytes.NewBuffer(c.Body())

	req, err := http.NewRequest(c.Method(), url, reqBodyBuffer)

	if err != nil {
		errorLog.Println("Error creating new request:", err.Error())
		return c.Status(fiber.StatusInternalServerError).SendStatus(fiber.StatusInternalServerError)
	}
	client := &http.Client{}

	var headersString string
	// Copy headers from Fiber context to the new http.Request
	for k, v := range c.GetReqHeaders() {
		for _, vv := range v {
			headersString += fmt.Sprintf("\t%s: %s\r\n", k, vv)
			switch k {
			case
				"If-None-Match",
				"If-Modified-Since",
				"If-Range",
				"X-Cloud-Trace-Context",
				"X-Forwarded-Proto",
				"X-Forwarded-For",
				"Forwarded",
				"Sec-Ch-Ua-Platform:",
				"Sec-Ch-Ua",
				"Sec-Ch-Ua-Mobile",
				"Sec-Fetch-Site",
				"Sec-Fetch-Mode",
				"Sec-Fetch-Dest",
				"Priority",
				"Accept-Encoding":
				continue
			}
			switch k {
			case
				"Host",
				"Accept",
				"User-Agent",
				"Accept-Language":
				req.Header.Add(k, vv)
			}
			//req.Header.Add(k, vv)
		}
	}
	req.Header.Add("MINIMIRROR", "TRUE")

	// Copy query params
	q := req.URL.Query()
	for key, val := range c.Queries() {
		if key == "EXTERNAL_URL" {
			continue
		}
		q.Add(key, val)
	}
	req.URL.RawQuery = q.Encode()

	// Fetch Request
	resp, err := client.Do(req)

	if err != nil {
		if retry < MaxRetry {
			log.Printf(err.Error())
			log.Printf("retrying to mirror %s", url)
			return mirrorUrl(url, c, retry+1)
		}
		errorLog.Printf("Failed after %d retries, returning error", retry)
		errorLog.Println(err.Error())
		return c.Status(fiber.StatusInternalServerError).SendStatus(fiber.StatusInternalServerError)
	}

	// Retry if server error
	if resp.StatusCode >= 500 && resp.StatusCode < 600 && retry < MaxRetry {
		log.Printf("Status code %d, retrying to mirror %s", resp.StatusCode, url)
		return mirrorUrl(url, c, retry+1)
	}
	if retry >= MaxRetry {
		log.Printf("Max retry number reached, returning %d", resp.StatusCode)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf(err.Error())
		}
	}(resp.Body)

	for name, values := range resp.Header {
		for _, value := range values {
			c.Set(name, value)
		}
	}

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		errorLog.Println("Error reading response body:", err.Error())
		return c.Status(fiber.StatusInternalServerError).SendStatus(fiber.StatusInternalServerError)
	}

	// Replace preset strings
	if len(ReplaceItems) > 0 {
		for _, item := range ReplaceItems {
			body = []byte(strings.ReplaceAll(string(body), item.Old, item.New))
		}
	}

	// Replace domain with relative link
	body = []byte(strings.ReplaceAll(string(body), TargetDomain+"/", "/"))

	// Replace secondary domains if there are any with proxy link
	if len(SecondaryDomains) > 0 && !(SecondaryDomains[0] == "") {
		for _, secDomain := range SecondaryDomains {
			body = []byte(strings.ReplaceAll(string(body), secDomain, "/_EXTERNAL_?EXTERNAL_URL="+secDomain))
		}
	}

	return c.Status(resp.StatusCode).Send(body)
}

func handleInternalRequest(c *fiber.Ctx) error {
	// Form new URL
	newURL := c.Path()
	if TargetEndpoint != "" {
		newURL = TargetEndpoint + newURL
	} else {
		newURL = TargetDomain + newURL
	}

	return mirrorUrl(newURL, c, 0)
}

func handleExternalRequest(c *fiber.Ctx) error {
	return mirrorUrl(c.Query("EXTERNAL_URL"), c, 0)
}

func main() {
	if port == "" {
		port = "3000"
	}

	if ReplaceItemsString != "" {
		err := json.Unmarshal([]byte(ReplaceItemsString), &ReplaceItems)
		if err != nil {
			log.Fatal("Error during Unmarshal() of REPLACE: ", err)
		}
	}

	app := fiber.New()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		_ = <-c
		fmt.Println("Gracefully shutting down...")
		_ = app.Shutdown()
	}()

	app.All("/_EXTERNAL_", func(c *fiber.Ctx) error {
		return handleExternalRequest(c)
	})

	app.Get("/check", func(c *fiber.Ctx) error {
		return c.SendString("Ok")
	})

	app.All("/*", func(c *fiber.Ctx) error {
		return handleInternalRequest(c)
	})

	if err := app.Listen(":" + port); err != nil {
		log.Panic(err)
	}
	fmt.Println("Goodbye!")
}
