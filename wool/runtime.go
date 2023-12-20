package wool

func IsDebug() bool {
	return globalLogLevel >= DEBUG
}

func GlobalLogLevel() Loglevel {
	return globalLogLevel
}

func SetGlobalLogLevel(loglevel Loglevel) {
	globalLogLevel = loglevel
}

var (
	globalLogLevel = INFO
)
