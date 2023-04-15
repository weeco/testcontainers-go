# redpanda

## Adding this module to your project dependencies

Please run the following command to add the redpanda module to your Go dependencies:

```
go get github.com/testcontainers/testcontainers-go/modules/redpanda
```

## Usage example

<!--codeinclude-->
[Test for a redpanda container](../../modules/redpanda/redpanda_test.go) inside_block:redpandaCreateContainer
<!--/codeinclude-->

## Container Options

When starting the Redpanda container, you can pass options in a variadic way to configure it.

### Image

If you need to set a different Redpanda Docker image, you can use `testcontainers.WithImage` with a valid Docker image
for Postgres. E.g. `testcontainers.WithImage("docker.io/redis:7")`.

<!--codeinclude-->
[Use a different image](../../modules/redis/redis_test.go) inside_block:withImage
<!--/codeinclude-->
