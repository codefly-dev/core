//go:build amd64 && darwin

package manager

import (
	"fmt"

	"github.com/codefly-dev/core/resources"
)

func DownloadURL(p *resources.Agent) string {
	return fmt.Sprintf("https://github.com/codefly-dev/service-%s/releases/download/v%s/service-%s_%s_darwin_amd64.tar.gz", p.Name, p.Version, p.Name, p.Version)
}
