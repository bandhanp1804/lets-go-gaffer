// Package httpx holds HTTP client infrastructure shared by every outbound
// integration (FPL, news feeds, Voyage, Anthropic): retries with backoff,
// rate limiting, and typed errors.
//
// Compose them as retry(rateLimit(base)) so each retry attempt is also paced.
package httpx
