package gitx

import (
	"context"
	"encoding/json"
	"time"
)

func JSON(value any) string { b, _ := json.MarshalIndent(value, "", "  "); return string(b) }
func Timeout(seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}
