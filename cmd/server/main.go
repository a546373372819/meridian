package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	gravityapi "github.com/c12s/gravity/pkg/api"
	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/c12s/meridian/internal/handlers"
	"github.com/c12s/meridian/internal/metrics"
	"github.com/c12s/meridian/internal/nodes"
	"github.com/c12s/meridian/internal/services"
	"github.com/c12s/meridian/internal/store"
	"github.com/c12s/meridian/pkg/api"
	"github.com/c12s/meridian/pkg/appref"
	oortapi "github.com/c12s/oort/pkg/api"
	pulsar_api "github.com/c12s/pulsar/model/protobuf"
	"github.com/neo4j/neo4j-go-driver/v4/neo4j"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

func main() {
	neo4jAddress := os.Getenv("NEO4J_ADDRESS")
	driver, err := neo4j.NewDriver(fmt.Sprintf("bolt://%s", neo4jAddress), neo4j.NoAuth())
	if err != nil {
		log.Fatal(err)
	}
	dbName := os.Getenv("NEO4J_DB_NAME")

	quotas := store.NewResourceQuotaNeo4jStore(driver, dbName)
	apps := store.NewAppNeo4jStore(driver, dbName, quotas)
	namespaces := store.NewNamespaceNeo4jStore(driver, dbName, quotas, apps)
	loans := store.NewLoanNeo4jStore(driver, dbName)
	transfer := store.NewTransferNeo4jStore(driver, dbName)
	conn, err := grpc.NewClient(os.Getenv("PULSAR_ADDRESS"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	pulsar := pulsar_api.NewSeccompServiceClient(conn)
	connGravity, err := grpc.NewClient(os.Getenv("GRAVITY_ADDRESS"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer connGravity.Close()
	gravity := gravityapi.NewAgentQueueClient(connGravity)
	administrator, err := oortapi.NewAdministrationAsyncClient(os.Getenv("NATS_ADDRESS"))
	if err != nil {
		log.Fatalln(err)
	}
	connMagnetar, err := grpc.NewClient(os.Getenv("MAGNETAR_ADDRESS"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer connMagnetar.Close()
	magnetar := magnetarapi.NewMagnetarClient(connMagnetar)
	authorizer := services.NewAuthZService(os.Getenv("SECRET_KEY"))

	dispatcher := nodes.NewDispatcher(magnetar, gravity, authorizer)
	accessor := store.NewQuotaStoreAccessor(quotas)
	borrow := services.NewBorrowService(accessor, loans, transfer, dispatcher)
	terminate := services.NewTerminationService(accessor, apps, borrow, dispatcher)
	meridian := handlers.NewMeridianGrpcHandler(namespaces, apps, quotas, loans, pulsar, administrator,
		authorizer, dispatcher, borrow, terminate)

	// automatic stress-borrowing loop
	if addrs := os.Getenv("STAROMETRY_ADDRESSES"); addrs != "" {
		if source, err := metrics.NewGRPCSource(strings.Split(addrs, ",")...); err != nil {
			log.Println("stress borrowing disabled (metrics source):", err)
		} else {
			provider := metrics.NewUtilizationProvider(
				source,
				accessor,
				appref.AppID,
			)
			controller := services.NewStressController(provider, accessor, borrow, loans)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stressResources := []string{"mem"}
			if rs := os.Getenv("STRESS_RESOURCES"); rs != "" {
				stressResources = strings.Split(rs, ",")
			}
			
			interval := 30 * time.Second
			if iv := os.Getenv("STRESS_INTERVAL"); iv != "" {
				if d, err := time.ParseDuration(iv); err == nil {
					interval = d
				}
			}
			go controller.Run(ctx, stressResources, interval)
			log.Println("stress borrowing loop started; resources:", stressResources, "interval:", interval)
		}
	}

	// reconciliation loop
	if iv := os.Getenv("RECONCILE_INTERVAL"); iv != "" {
		if d, err := time.ParseDuration(iv); err != nil {
			log.Println("reconciler disabled: bad RECONCILE_INTERVAL:", err)
		} else {
			reconciler := services.NewReconciler(accessor, borrow)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go reconciler.Run(ctx, d)
			log.Println("reconciler started, interval", d)
		}
	}

	//s := grpc.NewServer()
	s := grpc.NewServer(grpc.UnaryInterceptor(handlers.GetAuthInterceptor()))
	api.RegisterMeridianServer(s, meridian)
	reflection.Register(s)

	lis, err := net.Listen("tcp", os.Getenv("LISTEN_ADDRESS"))
	if err != nil {
		log.Fatal(err)
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("server listening at %v", lis.Addr())
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	<-shutdown

	s.GracefulStop()
}
