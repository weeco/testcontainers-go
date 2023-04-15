package redpanda

import "github.com/testcontainers/testcontainers-go"

type options struct {
	// Superusers is a list of service account names.
	Superusers []string

	// KafkaEnableAuthorization is a flag to require authorization for Kafka connections.
	KafkaEnableAuthorization bool

	// KafkaAuthenticationMethod is either "none" for plaintext or "sasl"
	// for SASL (scram) authentication.
	KafkaAuthenticationMethod string

	// SchemaRegistryAuthenticationMethod is either "none" for no authentication
	// or "http_basic" for HTTP basic authentication.
	SchemaRegistryAuthenticationMethod string

	// ServiceAccounts is a map of username (key) to password (value) of users
	// that shall be created, so that you can use these to authenticate against
	// Redpanda (either for the Kafka API or Schema Registry HTTP access).
	ServiceAccounts map[string]string

	// Docker image and version
	Image string
}

func defaultOptions() options {
	return options{
		KafkaEnableAuthorization:           false,
		Superusers:                         []string{},
		KafkaAuthenticationMethod:          "none",
		SchemaRegistryAuthenticationMethod: "none",
		ServiceAccounts:                    make(map[string]string, 0),
		Image:                              "docker.redpanda.com/redpandadata/redpanda:v23.1.6",
	}
}

// Option is an option for the Redpanda container.
type Option func(*options)

func WithNewServiceAccount(username, password string) testcontainers.CustomizeRequestOption {
	return func(o *options) {
		o.ServiceAccounts[username] = password
	}
}

// WithSuperusers defines the superusers added to the redpanda config.
// By default, there are no superusers.
func WithSuperusers(superusers ...string) testcontainers.CustomizeRequestOption {
	return func(o *options) {
		o.Superusers = superusers
	}
}

// WithEnableSASL enables SASL scram sha authentication.
// By default, no authentication (plaintext) is used.
// When setting an authentication method, make sure to add users
// as well as authorize them using the WithSuperusers() option.
func WithEnableSASL() testcontainers.CustomizeRequestOption {
	return func(o *options) {
		o.KafkaAuthenticationMethod = "sasl"
	}
}

// WithEnableKafkaAuthorization enables authorization for connections on the Kafka API.
func WithEnableKafkaAuthorization() testcontainers.CustomizeRequestOption {
	return func(o *options) {
		o.KafkaEnableAuthorization = true
	}
}

func WithEnableSchemaRegistryHTTPBasicAuth() testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) {
		req.
	}
	return func(o *options) {
		o.SchemaRegistryAuthenticationMethod = "http_basic"
	}
}
