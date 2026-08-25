package ratelimit

import "net/url"

func parseTestURL(raw string) (*url.URL, error) { return url.Parse(raw) }
