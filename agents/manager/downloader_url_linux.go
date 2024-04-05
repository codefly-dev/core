//go:build amd64 && linux

package manager

import (
	"fmt"

	"github.com/codefly-dev/core/configurations"
)

func DownloadURL(p *configurations.Agent) string {
	return fmt.Sprintf("https://github.com/codefly-dev/service-%s/releases/download/v%s/service-%s_%s_linux_amd64.tar.gz", p.Name, p.Version, p.Name, p.Version)
}
