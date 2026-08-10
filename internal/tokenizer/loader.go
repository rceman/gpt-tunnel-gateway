package tokenizer

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// The exact o200k_base mergeable-ranks payload is embedded so admission never
// depends on a network, cache directory, or external tokenizer installation.
//
//go:embed o200k_base.tiktoken.gz
var o200kBasePayload embed.FS

type embeddedBPELoader struct{}

const o200kBaseURL = "https://openaipublic.blob.core.windows.net/encodings/o200k_base.tiktoken"

func (embeddedBPELoader) LoadTiktokenBpe(requested string) (map[string]int, error) {
	if requested != o200kBaseURL {
		return nil, fmt.Errorf("embedded loader does not provide %q", requested)
	}
	data, err := o200kBasePayload.ReadFile("o200k_base.tiktoken.gz")
	if err != nil {
		return nil, fmt.Errorf("read embedded %s payload: %w", EncodingName, err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open embedded %s payload: %w", EncodingName, err)
	}
	defer reader.Close()

	ranks := make(map[string]int)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), " ")
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed embedded %s payload", EncodingName)
		}
		token, err := base64.StdEncoding.DecodeString(parts[0])
		if err != nil {
			return nil, fmt.Errorf("decode embedded %s token: %w", EncodingName, err)
		}
		rank, err := strconv.Atoi(parts[1])
		if err != nil || rank < 0 {
			return nil, fmt.Errorf("decode embedded %s rank", EncodingName)
		}
		ranks[string(token)] = rank
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read embedded %s payload: %w", EncodingName, err)
	}
	if len(ranks) == 0 {
		return nil, fmt.Errorf("embedded %s payload is empty", EncodingName)
	}
	return ranks, nil
}
