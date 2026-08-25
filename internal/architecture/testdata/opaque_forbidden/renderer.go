package renderer

import "github.com/alx4j/ai4j/internal/lifecycle"

func Render(stream lifecycle.ProcessStream) string {
	content, _ := stream.OpaqueBytes()
	return string(content)
}
