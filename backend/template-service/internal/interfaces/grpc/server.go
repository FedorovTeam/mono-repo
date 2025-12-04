package grpc

import (
	"context"
	"fmt"
	"log"
	"net"
	"template-service/internal/usecase"
	pb "template-service/proto/templatepb"
	"time"

	"google.golang.org/grpc"
)

type DemoEntityServer struct {
	pb.UnimplementedDemoEntityServiceServer
	uc *usecase.DemoUseCase
}

func NewDemoEntityServer(uc *usecase.DemoUseCase) *DemoEntityServer {
	return &DemoEntityServer{uc: uc}
}

func (s *DemoEntityServer) CreateDemoEntity(ctx context.Context, req *pb.CreateDemoEntityRequest) (*pb.CreateDemoEntityResponse, error) {
	now := time.Now()
	traceID := "" // Можно прокинуть из метаданных запроса, если нужно
	entity, err := s.uc.CreateDemo(
		req.UserId,
		req.Value,
		"",     // filePath если нужен
		"demo", // bucketType
		"",     // prefix
		"",     // entityPath
		"eu",   // region (пример)
		now,
		traceID,
	)
	if err != nil {
		return nil, err
	}
	return &pb.CreateDemoEntityResponse{
		Id:       entity.ID,
		BucketId: string(entity.BucketID),
		RegionId: string(entity.RegionID),
	}, nil
}

func (s *DemoEntityServer) GetDemoEntity(ctx context.Context, req *pb.GetDemoEntityRequest) (*pb.GetDemoEntityResponse, error) {
	entity, err := s.uc.GetDemo(req.Id)
	if err != nil {
		return nil, err
	}
	return &pb.GetDemoEntityResponse{
		Id:       entity.ID,
		BucketId: string(entity.BucketID),
		RegionId: string(entity.RegionID),
		Value:    entity.Value,
	}, nil
}

func RunGRPCServer(ctx context.Context, uc *usecase.DemoUseCase, port int) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterDemoEntityServiceServer(grpcServer, NewDemoEntityServer(uc))
	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()
	log.Printf("gRPC server listening on :%d", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
