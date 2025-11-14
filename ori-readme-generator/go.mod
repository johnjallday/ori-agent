module github.com/johnallday/ori-readme-generator

go 1.24

require (
	github.com/hashicorp/go-plugin v1.6.2
	github.com/johnjallday/ori-agent v0.0.0
	google.golang.org/grpc v1.69.4
	google.golang.org/protobuf v1.36.4
)

replace github.com/johnjallday/ori-agent => ../
