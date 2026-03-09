#!/usr/bin/env python3
"""
Sandbox Performance Benchmark

Measures overhead of sandbox wrapper vs direct execution.
"""

import subprocess
import time
import statistics

WRAPPER = "/app/sandbox-wrapper"
ITERATIONS = 20

def run_direct(cmd):
    """Run command directly"""
    start = time.perf_counter()
    subprocess.run(cmd, capture_output=True)
    return time.perf_counter() - start

def run_sandboxed(cmd):
    """Run command through sandbox"""
    wrapper_cmd = [WRAPPER, "-cpu=5", "-mem=128", "-timeout=10s", "--"] + cmd
    start = time.perf_counter()
    subprocess.run(wrapper_cmd, capture_output=True)
    return time.perf_counter() - start

def benchmark(name, cmd):
    """Run benchmark for a command"""
    print(f"\n=== {name} ===")
    
    # Warmup
    for _ in range(3):
        run_direct(cmd)
        run_sandboxed(cmd)
    
    # Benchmark direct
    direct_times = []
    for _ in range(ITERATIONS):
        direct_times.append(run_direct(cmd))
    
    # Benchmark sandboxed
    sandbox_times = []
    for _ in range(ITERATIONS):
        sandbox_times.append(run_sandboxed(cmd))
    
    # Stats
    direct_avg = statistics.mean(direct_times) * 1000
    direct_std = statistics.stdev(direct_times) * 1000
    sandbox_avg = statistics.mean(sandbox_times) * 1000
    sandbox_std = statistics.stdev(sandbox_times) * 1000
    overhead = sandbox_avg - direct_avg
    overhead_pct = (overhead / direct_avg) * 100 if direct_avg > 0 else 0
    
    print(f"Direct:    {direct_avg:6.2f}ms ± {direct_std:5.2f}ms")
    print(f"Sandboxed: {sandbox_avg:6.2f}ms ± {sandbox_std:5.2f}ms")
    print(f"Overhead:  {overhead:6.2f}ms ({overhead_pct:+.1f}%)")
    
    return {
        'name': name,
        'direct_ms': direct_avg,
        'sandbox_ms': sandbox_avg,
        'overhead_ms': overhead,
        'overhead_pct': overhead_pct
    }

def main():
    print("=" * 60)
    print("SANDBOX PERFORMANCE BENCHMARK")
    print("=" * 60)
    print(f"Iterations: {ITERATIONS}")
    
    results = []
    
    # Test 1: Simple echo
    results.append(benchmark(
        "Echo (minimal)",
        ["echo", "hello"]
    ))
    
    # Test 2: Python startup
    results.append(benchmark(
        "Python startup",
        ["python3", "-c", "pass"]
    ))
    
    # Test 3: Python with import
    results.append(benchmark(
        "Python with imports",
        ["python3", "-c", "import json, os, sys"]
    ))
    
    # Test 4: Python calculation
    results.append(benchmark(
        "Python calculation",
        ["python3", "-c", "print(sum(range(10000)))"]
    ))
    
    # Summary
    print("\n" + "=" * 60)
    print("SUMMARY")
    print("=" * 60)
    print(f"{'Test':<25} {'Direct':>10} {'Sandbox':>10} {'Overhead':>12}")
    print("-" * 60)
    for r in results:
        print(f"{r['name']:<25} {r['direct_ms']:>8.2f}ms {r['sandbox_ms']:>8.2f}ms {r['overhead_ms']:>8.2f}ms ({r['overhead_pct']:+.0f}%)")
    
    avg_overhead = statistics.mean([r['overhead_ms'] for r in results])
    print("-" * 60)
    print(f"Average overhead: {avg_overhead:.2f}ms")

if __name__ == "__main__":
    main()
