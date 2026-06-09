// Package main 实现 SWE-bench Lite benchmark runner，
// 用于评估 harness9 在真实 GitHub Issue 修复任务上的 Agent 能力。
//
// 用法:
//
//	go run ./cmd/swebench --dataset swe-bench-lite.jsonl --sample 10 --output ./results
//
// 环境变量:
//
//	OPENAI_API_KEY   LLM API Key（必填）
//	LLM_MODEL        模型名称（默认: openai/gpt-4o-mini）
//	SANDBOX_IMAGE    Docker 镜像（推荐: python:3.11-slim，默认: ubuntu:22.04）
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/harness9/internal/env"
)

func main() {
	// 加载 .env 文件（系统环境变量优先）
	_ = env.Load(".env")

	cfg := Config{}
	flag.StringVar(&cfg.DatasetPath, "dataset", "", "SWE-bench Lite JSONL 文件路径（必填）")
	flag.IntVar(&cfg.SampleN, "sample", 10, "每个 repo 抽取的 instance 数量")
	flag.StringVar(&cfg.OutputDir, "output", "./swebench-results", "输出目录")
	flag.IntVar(&cfg.MaxTurns, "max-turns", 30, "每个 instance 最大 LLM Turn 数")
	flag.IntVar(&cfg.Parallel, "parallel", 1, "并发 instance 数")
	flag.BoolVar(&cfg.Resume, "resume", false, "跳过已有结果的 instance（断点续跑）")
	flag.IntVar(&cfg.TimeoutMins, "timeout", 10, "单个 instance 超时（分钟）")
	flag.StringVar(&cfg.Model, "model", "", "LLM 模型名称（默认使用 LLM_MODEL 环境变量）")
	flag.Parse()

	if cfg.DatasetPath == "" {
		fmt.Fprintln(os.Stderr, "错误: --dataset 必填")
		flag.Usage()
		os.Exit(1)
	}

	// Preflight checks
	if err := preflight(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "启动检查失败: %v\n", err)
		os.Exit(1)
	}

	// 加载数据集
	allInstances, err := loadDataset(cfg.DatasetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载数据集失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "数据集加载完成: %d 条 instances\n", len(allInstances))

	// 按 repo 采样
	instances := sampleByRepo(allInstances, cfg.SampleN, time.Now().UnixNano())
	fmt.Fprintf(os.Stderr, "采样完成: %d 条（每 repo 最多 %d 条）\n", len(instances), cfg.SampleN)

	// 加载已有结果（--resume 模式）
	predictionsPath := filepath.Join(cfg.OutputDir, "predictions.jsonl")
	skipIDs := make(map[string]bool)
	if cfg.Resume {
		skipIDs, err = loadExistingIDs(predictionsPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取已有结果失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "断点续跑: 跳过 %d 个已有结果\n", len(skipIDs))
	}

	// 创建输出目录
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "创建输出目录失败: %v\n", err)
		os.Exit(1)
	}

	// 信号处理（Ctrl+C 优雅退出）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\n收到终止信号，等待当前 instance 完成...")
		cancel()
	}()

	// 并发执行
	sem := semaphore.NewWeighted(int64(cfg.Parallel))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []RunResult
	start := time.Now()

	for _, inst := range instances {
		if skipIDs[inst.InstanceID] {
			fmt.Fprintf(os.Stderr, "[skip] %s\n", inst.InstanceID)
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}
		wg.Add(1)
		go func(inst Instance) {
			defer sem.Release(1)
			defer wg.Done()

			fmt.Fprintf(os.Stderr, "[start] %s\n", inst.InstanceID)
			result := runInstance(ctx, inst, cfg)

			mu.Lock()
			results = append(results, result)
			if appendErr := appendPrediction(predictionsPath, Prediction{
				InstanceID: inst.InstanceID,
				ModelPatch: result.Patch,
			}); appendErr != nil {
				fmt.Fprintf(os.Stderr, "[error] 写入 predictions 失败 (%s): %v\n", inst.InstanceID, appendErr)
			}
			mu.Unlock()

			if result.Error != nil {
				fmt.Fprintf(os.Stderr, "[error] %s (%s): %v\n", inst.InstanceID, result.Duration.Round(time.Second), result.Error)
			} else {
				fmt.Fprintf(os.Stderr, "[done]  %s (%s) patch=%d bytes\n", inst.InstanceID, result.Duration.Round(time.Second), len(result.Patch))
			}
		}(inst)
	}
	wg.Wait()

	// 写汇总
	end := time.Now()
	if err := writeSummary(cfg.OutputDir, results, start, end); err != nil {
		fmt.Fprintf(os.Stderr, "写入摘要失败: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "\n完成！结果已写入 %s\n", cfg.OutputDir)
	fmt.Fprintf(os.Stderr, "总实例: %d，耗时: %s\n", len(results), end.Sub(start).Round(time.Second))
}

// preflight 在启动前验证必要条件，任一失败则终止程序。
func preflight(cfg Config) error {
	if cfg.Parallel <= 0 {
		return fmt.Errorf("--parallel 必须 >= 1，当前值: %d", cfg.Parallel)
	}
	if cfg.SampleN <= 0 {
		return fmt.Errorf("--sample 必须 >= 1，当前值: %d", cfg.SampleN)
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		return fmt.Errorf("OPENAI_API_KEY 未配置")
	}
	if _, err := os.Stat(cfg.DatasetPath); err != nil {
		return fmt.Errorf("dataset 文件不可读: %w", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git 命令不可用: %w", err)
	}
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer checkCancel()
	if out, err := exec.CommandContext(checkCtx, "docker", "info").CombinedOutput(); err != nil {
		return fmt.Errorf("Docker daemon 不可达: %w\n%s", err, out)
	}
	parent := filepath.Dir(cfg.OutputDir)
	if _, err := os.Stat(parent); err != nil {
		return fmt.Errorf("输出目录的父路径不存在: %w", err)
	}
	return nil
}
