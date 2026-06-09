// Package main 实现 SWE-bench Lite benchmark runner，
// 用于评估 harness9 在真实 GitHub Issue 修复任务上的 Agent 能力。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"time"
)

// Instance 是 SWE-bench Lite 数据集的一条记录（JSONL 格式）。
type Instance struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	HintsText        string `json:"hints_text"`
}

// Prediction 是写入 predictions.jsonl 的一条记录（官方兼容格式）。
type Prediction struct {
	InstanceID string `json:"instance_id"`
	ModelPatch string `json:"model_patch"`
}

// RunResult 记录单个 instance 的运行结果，供汇总使用。
type RunResult struct {
	Instance Instance
	Patch    string
	Error    error
	Duration time.Duration
}

// loadDataset 从 JSONL 文件加载所有 instance。
func loadDataset(path string) ([]Instance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开数据集失败: %w", err)
	}
	defer f.Close()

	var instances []Instance
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var inst Instance
		if err := json.Unmarshal([]byte(line), &inst); err != nil {
			return nil, fmt.Errorf("解析 JSONL 行失败: %w", err)
		}
		instances = append(instances, inst)
	}
	return instances, scanner.Err()
}

// sampleByRepo 按 repo 分组，每组随机取最多 n 条，打乱后返回。
func sampleByRepo(instances []Instance, n int, seed int64) []Instance {
	byRepo := make(map[string][]Instance)
	for _, inst := range instances {
		byRepo[inst.Repo] = append(byRepo[inst.Repo], inst)
	}

	// 对 repo 名排序，确保相同 seed 产生相同输出（Go map 遍历非确定性）
	repos := make([]string, 0, len(byRepo))
	for repo := range byRepo {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	rng := rand.New(rand.NewSource(seed))

	var sampled []Instance
	for _, repo := range repos {
		group := byRepo[repo]
		rng.Shuffle(len(group), func(i, j int) { group[i], group[j] = group[j], group[i] })
		if len(group) > n {
			group = group[:n]
		}
		sampled = append(sampled, group...)
	}
	rng.Shuffle(len(sampled), func(i, j int) { sampled[i], sampled[j] = sampled[j], sampled[i] })
	return sampled
}
