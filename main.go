package main

import (
	"github.com/valyala/fasthttp"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

var timeout = getPositiveEnvInt("TIMEOUT", 5)
var retries = getPositiveEnvInt("RETRIES", 5)
var port = getEnvString("PORT", "8080")

var client *fasthttp.Client

func getPositiveEnvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}

	return parsed
}

func getEnvString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}

func isVersionSegment(segment string) bool {
	if len(segment) < 2 || segment[0] != 'v' {
		return false
	}

	_, err := strconv.Atoi(segment[1:])
	return err == nil
}

func main() {
	h := requestHandler
	
	client = &fasthttp.Client{
		ReadTimeout: time.Duration(timeout) * time.Second,
		MaxIdleConnDuration: 60 * time.Second,
	}

	if err := fasthttp.ListenAndServe(":"+port, h); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	val, ok := os.LookupEnv("KEY")

	if ok && string(ctx.Request.Header.Peek("PROXYKEY")) != val {
		ctx.SetStatusCode(407)
		ctx.SetBody([]byte("Missing or invalid PROXYKEY header."))
		return
	}

	requestPath := strings.Trim(string(ctx.Path()), "/")
	if len(requestPath) == 0 {
		ctx.SetStatusCode(400)
		ctx.SetBody([]byte("URL format invalid."))
		return
	}

	response := makeRequest(ctx, 1)

	defer fasthttp.ReleaseResponse(response)

	body := response.Body()
	ctx.SetBody(body)
	ctx.SetStatusCode(response.StatusCode())
	response.Header.VisitAll(func (key, value []byte) {
		ctx.Response.Header.Set(string(key), string(value))
	})
}

func makeRequest(ctx *fasthttp.RequestCtx, attempt int) *fasthttp.Response {
	if attempt > retries {
		resp := fasthttp.AcquireResponse()
		resp.SetBody([]byte("Proxy failed to connect. Please try again."))
		resp.SetStatusCode(500)

		return resp
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.Header.SetMethod(string(ctx.Method()))
	requestPath := strings.Trim(string(ctx.Path()), "/")
	segments := strings.Split(requestPath, "/")
	var targetURL string
	if len(segments) == 2 && segments[0] == "users" && segments[1] != "" {
		targetURL = "https://users.roblox.com/v1/users/" + segments[1]
	} else if len(segments) >= 3 && isVersionSegment(segments[0]) && segments[1] == "users" {
		targetURL = "https://users.roblox.com/" + requestPath
	} else {
		parts := strings.SplitN(requestPath, "/", 2)
		if len(parts) == 1 {
			targetURL = "https://www.roblox.com/" + parts[0]
		} else {
			targetURL = "https://" + parts[0] + ".roblox.com/" + parts[1]
		}
	}

	if len(ctx.URI().QueryString()) > 0 {
		targetURL += "?" + string(ctx.URI().QueryString())
	}

	req.SetRequestURI(targetURL)
	req.SetBody(ctx.Request.Body())
	ctx.Request.Header.VisitAll(func (key, value []byte) {
		if strings.EqualFold(string(key), "Host") || strings.EqualFold(string(key), "PROXYKEY") {
			return
		}
		req.Header.Set(string(key), string(value))
	})
	req.Header.Set("User-Agent", "RoProxy")
	req.Header.Del("Roblox-Id")
	resp := fasthttp.AcquireResponse()

	err := client.Do(req, resp)

	if err != nil {
		log.Printf("Upstream request failed (attempt %d/%d) for %s: %v", attempt, retries, targetURL, err)
		fasthttp.ReleaseResponse(resp)
		return makeRequest(ctx, attempt + 1)
	} else {
		return resp
	}
}
