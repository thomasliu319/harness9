// cmd/otel-test：最小化 OTEL 上报验证工具
// 直接读取 .env 中的 Langfuse 配置，创建一个测试 span 并同步推送，
// 用于排查 harness9 主程序中 OTEL 不上报的根因。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/harness9/internal/env"
)

func main() {
	cwd, _ := os.Getwd()
	_ = env.Load(filepath.Join(cwd, ".env"))

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	headersRaw := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")

	if endpoint == "" {
		log.Fatal("OTEL_EXPORTER_OTLP_ENDPOINT 未设置，请检查 .env 文件")
	}

	tracesURL := strings.TrimSuffix(endpoint, "/") + "/v1/traces"
	fmt.Printf("→ endpoint:  %s\n", tracesURL)
	fmt.Printf("→ headers:   %s\n", headersRaw)
	fmt.Println()

	// 解析 headers
	headers := parseHeaders(headersRaw)

	// 建立错误 handler
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Printf("[OTEL ERROR] %v", err)
	}))

	ctx := context.Background()

	// 创建 OTLP exporter（与 harness9 完全相同的配置）
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(tracesURL),
	}
	if strings.HasPrefix(tracesURL, "http://") {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		log.Fatalf("创建 exporter 失败: %v", err)
	}
	fmt.Println("✅ Exporter 初始化成功")

	res, _ := resource.New(ctx, resource.WithAttributes(semconv.ServiceName("harness9")))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// 创建测试 span
	tracer := tp.Tracer("harness9")
	ctx2, span := tracer.Start(ctx, "harness9.otel-test")
	span.SetAttributes(
		attribute.String("langfuse.input", "otel-test diagnostic from Go SDK"),
		attribute.String("langfuse.output", "success"),
		attribute.String("test.time", time.Now().Format(time.RFC3339)),
	)
	time.Sleep(100 * time.Millisecond)
	span.End()
	fmt.Println("✅ Span 创建并结束")
	_ = ctx2

	// ForceFlush（5s 超时）
	fmt.Print("→ ForceFlush... ")
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tp.ForceFlush(flushCtx); err != nil {
		fmt.Printf("❌ 失败: %v\n", err)
	} else {
		fmt.Println("✅ 成功")
	}

	// Shutdown
	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := tp.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Shutdown error] %v", err)
	}

	fmt.Println()
	fmt.Println("🔍 请在 Langfuse → Tracing 页面刷新，查找名为 'harness9.otel-test' 的 span")
}

func parseHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	if raw == "" {
		return headers
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		idx := strings.Index(pair, "=")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(pair[:idx])
		v := strings.TrimSpace(pair[idx+1:])
		if k != "" {
			headers[k] = v
		}
	}
	return headers
}
