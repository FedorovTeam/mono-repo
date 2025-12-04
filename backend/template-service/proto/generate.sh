protoc -I. \
  --go_out=./templatepb --go_opt=paths=source_relative \
  --go-grpc_out=./templatepb --go-grpc_opt=paths=source_relative \
  demo_entity.proto