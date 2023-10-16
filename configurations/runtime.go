package configurations

var dryRun bool

func SetDryRun(d bool) {
	dryRun = d
}

func DryRun() bool {
	return dryRun
}
