package wool

func IsDebug() bool {
	return globalLogLevel >= DEBUG

}

var (
	globalLogLevel = INFO
)

func SetLogLevel(loglevel Loglevel) {
	globalLogLevel = loglevel
}
