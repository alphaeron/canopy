package shonin

import (
	"github.com/alphaeron/kit/internal/kitfile"
)

type Fetcher interface {
	GetRules() (string, map[string]kitfile.Rules)
}
