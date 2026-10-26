package resources

// EndpointDeliveryDirectoryKey names the stable directory of replaceable,
// generation-bound host route projections written by the CLI/cell delivery
// owner and read by the SDK. It is not a snapshot of runtime configuration or
// credentials. Consumers use the SDK accessor, never this environment key.
const EndpointDeliveryDirectoryKey = "CODEFLY__ENDPOINT_DELIVERY_DIRECTORY"
