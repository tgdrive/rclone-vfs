package link

import (
	"net/url"
	"path"
)

func StripURL(u string, stripQuery, stripDomain bool) string {
	if !stripQuery && !stripDomain {
		return u
	}

	parsedURL, err := url.Parse(u)
	if err != nil {
		return u
	}

	if stripQuery {
		parsedURL.RawQuery = ""
	}
	if stripDomain {
		parsedURL.Scheme = ""
		parsedURL.Host = ""
		parsedURL.User = nil
		parsedURL.Fragment = ""
	}

	return parsedURL.String()
}

func ShardedPath(fileHash string, level int) string {
	if level <= 0 || len(fileHash) < 2 {
		return fileHash
	}
	parts := make([]string, 0, level+1)
	for i := range level {
		start := i * 2
		end := (i + 1) * 2
		if end > len(fileHash) {
			break
		}
		parts = append(parts, fileHash[start:end])
	}
	parts = append(parts, fileHash)
	return path.Join(parts...)
}
