package api

import _ "embed"

// OpenAPISpec is the hand-maintained API description served at /api/v1/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
