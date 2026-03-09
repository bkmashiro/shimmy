// worker_integration.go - Integration guide for shimmy worker
//
// To enable sandboxing in shimmy, modify internal/execution/worker/worker.go:
//
// 1. Add import:
//    "github.com/lambda-feedback/shimmy/internal/sandbox"
//
// 2. Modify createCmd function:
//
//    func createCmd(ctx context.Context, config StartConfig) *exec.Cmd {
//        // Check if sandboxing is enabled (via env or config)
//        if os.Getenv("SHIMMY_SANDBOX") == "1" {
//            sandboxCfg := sandbox.DefaultConfig()
//            // Override from config if needed
//            if config.SandboxConfig != nil {
//                sandboxCfg = *config.SandboxConfig
//            }
//            cmd := sandbox.WrapCommandContext(ctx, config.Cmd, config.Args, sandboxCfg)
//            cmd.Env = append(os.Environ(), config.Env...)
//            cmd.Dir = config.Cwd
//            initCmd(cmd)
//            return cmd
//        }
//        
//        // Original code for non-sandboxed execution
//        cmd := exec.CommandContext(ctx, config.Cmd, config.Args...)
//        // ... rest of original code
//    }
//
// 3. Add SandboxConfig to StartConfig in models.go:
//
//    type StartConfig struct {
//        Cmd           string
//        Cwd           string
//        Args          []string
//        Env           []string
//        SandboxConfig *sandbox.Config  // Add this
//    }
package sandbox
