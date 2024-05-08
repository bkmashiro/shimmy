package lambda

// ProxySource represents the source of a lambda request.
type ProxySource string

const (
	// ProxySourceApiGatewayV1 represents an API Gateway v1 request.
	ProxySourceApiGatewayV1 ProxySource = "API_GW_V1"

	// ProxySourceApiGatewayV2 represents an API Gateway v2 request.
	ProxySourceApiGatewayV2 ProxySource = "API_GW_V2"

	// ProxySourceAlb represents an Application Load Balancer request.
	ProxySourceAlb ProxySource = "ALB"
)

func (p ProxySource) String() string {
	return string(p)
}

type Config struct {
	// ProxySource is the source of the AWS Lambda event.
	ProxySource ProxySource `conf:"lambda_proxy_source"`
}
