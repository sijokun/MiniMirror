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

type TargetInfo struct {
	Domain           string   `json:"domain"`
	Target           string   `json:"target"`
	SecondaryDomains []string `json:"secondary_domains"`
}

var (
	config       map[string]TargetInfo
	configString = os.Getenv("CONFIG")
	port         = os.Getenv("PORT")
	hostHeader   = os.Getenv("HOST_HEADER")
)

var errorLog = log.New(os.Stderr, "", 0)

const MaxRetry = 3

func mirrorUrl(url string, c *fiber.Ctx, retry int8, conf TargetInfo) error {
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
				"Host",
				"Accept",
				"User-Agent",
				"Accept-Language":
				req.Header.Add(k, vv)
			}
		}
	}
	req.Header.Add("TON_MIRROR", "TRUE")

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
			return mirrorUrl(url, c, retry+1, conf)
		}
		errorLog.Printf("Failed after %d retries, returning error", retry)
		errorLog.Println(err.Error())
		return c.Status(fiber.StatusInternalServerError).SendStatus(fiber.StatusInternalServerError)
	}

	// Retry if server error
	if resp.StatusCode >= 500 && resp.StatusCode < 600 && retry < MaxRetry {
		log.Printf("Status code %d, retrying to mirror %s", resp.StatusCode, url)
		return mirrorUrl(url, c, retry+1, conf)
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

	//// Replace preset strings
	//if len(ReplaceItems) > 0 {
	//	for _, item := range ReplaceItems {
	//		body = []byte(strings.ReplaceAll(string(body), item.Old, item.New))
	//	}
	//}

	// Replace domain with relative link
	body = []byte(strings.ReplaceAll(string(body), conf.Domain+"/", "/"))

	// Replace secondary domains if there are any with proxy link
	if len(conf.SecondaryDomains) > 0 {
		for _, secDomain := range conf.SecondaryDomains {
			body = []byte(strings.ReplaceAll(string(body), secDomain, "/_EXTERNAL_?EXTERNAL_URL="+secDomain))
		}
	}

	return c.Status(resp.StatusCode).Send(body)
}

func handleInternalRequest(c *fiber.Ctx) error {
	// Form new URL
	newURL := c.Path()

	host := c.GetReqHeaders()[hostHeader]
	if len(host) == 0 {
		return c.Status(400).Send([]byte("Host header is not set"))
	}

	conf, ok := config[host[0]]
	if !ok {
		log.Printf("Config for host %s not found", host[0])
		return c.Status(404).Send([]byte("Host config not found"))
	}

	newURL = conf.Target + newURL

	return mirrorUrl(newURL, c, 0, config["sota.ton"])
}

func handleExternalRequest(c *fiber.Ctx) error {
	return mirrorUrl(c.Query("EXTERNAL_URL"), c, 0, config["sota.ton"])
}

func main() {
	if port == "" {
		port = "3000"
	}

	// Config
	if configString != "" {
		err := json.Unmarshal([]byte(configString), &config)
		if err != nil {
			log.Fatal("Error during Unmarshal() of config: ", err)
		}
	} else {
		log.Fatal("Config is required")
	}

	// Default host header
	if hostHeader == "" {
		hostHeader = "Host"
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
