package redpanda

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/template"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/redpanda-data/redpanda/src/go/rpk/pkg/api/admin"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	//go:embed mounts/redpanda.yaml.tpl
	nodeConfigTpl string

	//go:embed mounts/bootstrap.yaml.tpl
	bootstrapConfigTpl string

	//go:embed mounts/entrypoint-tc.sh
	entrypoint []byte

	defaultKafkaAPIPort       = "9092/tcp"
	defaultAdminAPIPort       = "9644/tcp"
	defaultSchemaRegistryPort = "8081/tcp"
)

// Container represents the redpanda container type used in the module
type Container struct {
	testcontainers.Container
}

// StartContainer creates an instance of the redpanda container.
// Terminate must be called when the container is no longer needed.
func StartContainer(ctx context.Context, opts ...Option) (*Container, error) {
	// 1. Gather all config options (defaults and then apply provided options)
	settings := defaultOptions()
	for _, opt := range opts {
		opt(&settings)
	}

	// 2. Create temporary entrypoint file. We need a custom entrypoint that waits
	// until the actual Redpanda node config is mounted. Once the redpanda config is
	// mounted we will call the original entrypoint with the same parameters.
	// We have to do this kind of two-step process, because we need to know the mapped
	// port, so that we can use this in Redpanda's advertised listeners configuration for
	// the Kafka API.
	entrypointFile, err := createEntrypointTmpFile()
	if err != nil {
		return nil, fmt.Errorf("failed to create entrypoint file: %w", err)
	}

	// Bootstrap config file contains cluster configurations which will only be considered
	// the very first time you start a cluster.
	bootstrapConfigFile, err := createBootstrapConfigFile(settings)
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap config file: %w", err)
	}

	// 3. Create container request and start container
	containerReq := testcontainers.ContainerRequest{
		Image: settings.Image,
		User:  "root:root",
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      entrypointFile.Name(),
				ContainerFilePath: "/entrypoint-tc.sh",
				FileMode:          700,
			},
			{
				HostFilePath:      bootstrapConfigFile.Name(),
				ContainerFilePath: "/etc/redpanda/.bootstrap.yaml",
				FileMode:          700,
			},
		},
		ExposedPorts: []string{
			defaultKafkaAPIPort,
			defaultAdminAPIPort,
			defaultSchemaRegistryPort,
		},
		Entrypoint: []string{},
		Cmd: []string{
			"/entrypoint-tc.sh",
			"redpanda",
			"start",
			"--mode=dev-container",
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerReq,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	// 4. Get mapped port for the Kafka API, so that we can render and then mount
	// the Redpanda config with the advertised Kafka address.
	hostIP, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	kafkaPort, err := container.MappedPort(ctx, nat.Port(defaultKafkaAPIPort))
	if err != nil {
		return nil, fmt.Errorf("failed to get mapped Kafka port: %w", err)
	}

	// 5. Render redpanda.yaml config and mount it.
	nodeConfig, err := renderNodeConfig(settings, hostIP, kafkaPort.Int())
	if err != nil {
		return nil, fmt.Errorf("failed to render node config: %w", err)
	}

	err = container.CopyToContainer(
		ctx,
		nodeConfig,
		"/etc/redpanda/redpanda.yaml",
		700,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to copy redpanda.yaml into container: %w", err)
	}

	// 6. Wait until Redpanda is ready to serve requests
	err = wait.ForAll(
		wait.ForLog("Successfully started Redpanda!").WithPollInterval(100*time.Millisecond),
		wait.ForHTTP("/v1/cluster/health_overview").
			WithPort(nat.Port(defaultAdminAPIPort)).
			WithResponseMatcher(func(body io.Reader) bool {
				response, err := io.ReadAll(body)
				if err != nil {
					return false
				}

				healthOverview := admin.ClusterHealthOverview{}
				if err := json.Unmarshal(response, &healthOverview); err != nil {
					return false
				}

				return healthOverview.IsHealthy
			}).
			WithPollInterval(100*time.Millisecond),
	).WaitUntilReady(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for Redpanda readiness: %w", err)
	}

	// 7. Create Redpanda Service Accounts if configured to do so.
	if len(settings.ServiceAccounts) > 0 {
		adminAPIPort, err := container.MappedPort(ctx, nat.Port(defaultAdminAPIPort))
		if err != nil {
			return nil, fmt.Errorf("failed to get mapped Admin API port: %w", err)
		}

		adminAPIUrl := fmt.Sprintf("http://%v:%d", hostIP, adminAPIPort.Int())
		adminCl, err := admin.NewAdminAPI([]string{adminAPIUrl}, admin.BasicCredentials{}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create new admin api client: %w", err)
		}

		for username, password := range settings.ServiceAccounts {
			if err := adminCl.CreateUser(ctx, username, password, admin.ScramSha256); err != nil {
				return nil, fmt.Errorf("failed to create service account with username %q: %w", username, err)
			}
		}
	}

	return &Container{Container: container}, nil
}

// KafkaSeedBroker returns the seed broker that should be used for connecting
// to the Kafka API with your Kafka client. It'll be returned in the format:
// "host:port" - for example: "localhost:55687".
func (c *Container) KafkaSeedBroker(ctx context.Context) (string, error) {
	return c.getMappedHostPort(ctx, nat.Port(defaultKafkaAPIPort))
}

// AdminAPIAddress returns the address to the Redpanda Admin API. This
// is an HTTP-based API and thus the returned format will be: http://host:port.
func (c *Container) AdminAPIAddress(ctx context.Context) (string, error) {
	hostPort, err := c.getMappedHostPort(ctx, nat.Port(defaultAdminAPIPort))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%v", hostPort), nil
}

// SchemaRegistryAddress returns the address to the schema registry API. This
// is an HTTP-based API and thus the returned format will be: http://host:port.
func (c *Container) SchemaRegistryAddress(ctx context.Context) (string, error) {
	hostPort, err := c.getMappedHostPort(ctx, nat.Port(defaultSchemaRegistryPort))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%v", hostPort), nil
}

// getMappedHostPort returns the mapped host and port a given nat.Port following
// this format: "host:port". The mapped port is the port that is accessible from
// the host system and is remapped to the given container port.
func (c *Container) getMappedHostPort(ctx context.Context, port nat.Port) (string, error) {
	hostIP, err := c.Host(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get hostIP: %w", err)
	}

	mappedPort, err := c.MappedPort(ctx, port)
	if err != nil {
		return "", fmt.Errorf("failed to get mapped port: %w", err)
	}

	return fmt.Sprintf("%v:%d", hostIP, mappedPort.Int()), nil
}

// createEntrypointTmpFile returns a temporary file with the custom entrypoint
// that awaits the actual Redpanda config after the container has been started,
// before it's going to start the Redpanda process.
func createEntrypointTmpFile() (*os.File, error) {
	entrypointTmpFile, err := os.CreateTemp("", "")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(entrypointTmpFile.Name(), entrypoint, 0o700); err != nil {
		return nil, err
	}

	return entrypointTmpFile, nil
}

// createBootstrapConfigFile renders the config template for the .bootstrap.yaml config,
// which configures Redpanda's cluster properties.
// Reference: https://docs.redpanda.com/docs/reference/cluster-properties/
func createBootstrapConfigFile(settings options) (*os.File, error) {
	bootstrapTplParams := redpandaBootstrapConfigTplParams{
		Superusers:                  settings.Superusers,
		KafkaAPIEnableAuthorization: settings.KafkaEnableAuthorization,
	}

	tpl, err := template.New("bootstrap.yaml").Parse(bootstrapConfigTpl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redpanda config file template: %w", err)
	}

	var bootstrapConfig bytes.Buffer
	if err := tpl.Execute(&bootstrapConfig, bootstrapTplParams); err != nil {
		return nil, fmt.Errorf("failed to render redpanda bootstrap config template: %w", err)
	}

	bootstrapTmpFile, err := os.CreateTemp("", "")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(bootstrapTmpFile.Name(), bootstrapConfig.Bytes(), 0o700); err != nil {
		return nil, err
	}

	return bootstrapTmpFile, nil
}

// renderNodeConfig renders the redpanda.yaml node config and retuns it as
// byte array.
func renderNodeConfig(settings options, hostIP string, advertisedKafkaPort int) ([]byte, error) {
	tplParams := redpandaConfigTplParams{
		KafkaAPI: redpandaConfigTplParamsKafkaAPI{
			AdvertisedHost:       hostIP,
			AdvertisedPort:       advertisedKafkaPort,
			AuthenticationMethod: settings.KafkaAuthenticationMethod,
			EnableAuthorization:  settings.KafkaEnableAuthorization,
		},
		SchemaRegistry: redpandaConfigTplParamsSchemaRegistry{
			AuthenticationMethod: settings.SchemaRegistryAuthenticationMethod,
		},
	}

	ncTpl, err := template.New("redpanda.yaml").Parse(nodeConfigTpl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redpanda config file template: %w", err)
	}

	var redpandaYaml bytes.Buffer
	if err := ncTpl.Execute(&redpandaYaml, tplParams); err != nil {
		return nil, fmt.Errorf("failed to render redpanda node config template: %w", err)
	}

	return redpandaYaml.Bytes(), nil
}

type redpandaBootstrapConfigTplParams struct {
	Superusers                  []string
	KafkaAPIEnableAuthorization bool
}

type redpandaConfigTplParams struct {
	KafkaAPI       redpandaConfigTplParamsKafkaAPI
	SchemaRegistry redpandaConfigTplParamsSchemaRegistry
}

type redpandaConfigTplParamsKafkaAPI struct {
	AdvertisedHost       string
	AdvertisedPort       int
	AuthenticationMethod string
	EnableAuthorization  bool
}

type redpandaConfigTplParamsSchemaRegistry struct {
	AuthenticationMethod string
}
