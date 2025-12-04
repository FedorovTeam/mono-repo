# Контракты (gRPC, NATS)

## Пример gRPC контракта
```proto
syntax = "proto3";
package template;

service DemoService {
  rpc CreateDemoEntity (CreateDemoEntityRequest) returns (CreateDemoEntityResponse);
  rpc GetDemoEntity (GetDemoEntityRequest) returns (GetDemoEntityResponse);
  rpc GetDemo (DemoRequest) returns (DemoResponse);
}

message CreateDemoEntityRequest {
  string bucket_id = 1;
  string region_id = 2;
  string value = 3;
}
message CreateDemoEntityResponse {
  string id = 1;
}

message GetDemoEntityRequest {
  string id = 1;
}
message GetDemoEntityResponse {
  string id = 1;
  string bucket_id = 2;
  string region_id = 3;
  string value = 4;
}

message DemoRequest {
  string id = 1;
}

message DemoResponse {
  string id = 1;
  string value = 2;
}
```

## NATS JetStream Topics

- Main Event Subject: `template.demo-event.v1`
- DLQ Subject: `template.demo-event.dlq.v1`
- Stream: `template-demo-stream`
- Названия subject'ов можно изменить в `config/config.yaml` (параметры `demo_subject` и `demo_dlq_subject`).

### DemoEvent Structure
```json
{
  "id": "string",
  "action": "string",
  "trace_id": "string",
  "timestamp": 1714337534
}
```

### Headers
- `trace_id`: string
- `correlation_id`: string (обычно совпадает с id)

### DLQ
- Все сообщения, не обработанные после 5 попыток, попадают в subject `.dlq`.
- Подписчик на DLQ обязан логировать и алертить о сбоях.

## Пример NATS события
```json
{
  "id": "string",
  "action": "created",
  "trace_id": "string"
}
```
