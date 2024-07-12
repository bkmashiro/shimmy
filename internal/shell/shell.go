package shell

import (
	"context"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

type Shell struct {
	log     *zap.Logger
	fxApp   *fx.App
	options []fx.Option
}

func New(log *zap.Logger, options ...fx.Option) *Shell {
	return &Shell{
		log:     log,
		options: options,
	}
}

func (s *Shell) Run(ctx context.Context, options ...fx.Option) error {
	// 0. after run ends, flush the logger
	defer s.log.Sync()

	// 1. create shell context
	shellCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 2. create execution context
	appCtx, cancelApp := context.WithCancel(ctx)
	defer cancelApp()

	// 3. create fx application with app context
	fxApp := s.createFxApp(appCtx, options...)
	s.fxApp = fxApp

	// 4. create start context w/ timeout
	startCtx, cancelStart := context.WithTimeout(shellCtx, fxApp.StartTimeout())
	defer cancelStart()

	// 5. start the application, exit on error
	if err := fxApp.Start(startCtx); err != nil {
		return NewExitError(1)
	}

	// 6. wait for done signal by OS
	sig := <-fxApp.Wait()
	exitCode := sig.ExitCode

	// 7. cancel app context
	// cancelApp()

	// 8. create shutdown context
	stopCtx, cancelStop := context.WithTimeout(shellCtx, fxApp.StopTimeout())
	defer cancelStop()

	// 9. gracefully shutdown the app, exit on error
	if err := fxApp.Stop(stopCtx); err != nil {
		return NewExitError(1)
	}

	// 10. return with 0 exit code
	return NewExitError(exitCode)
}

func (s *Shell) createFxApp(ctx context.Context, options ...fx.Option) *fx.App {
	// 1. create fx application
	return fx.New(
		// 2. inject global execution context
		fx.Supply(fx.Annotate(ctx, fx.As(new(context.Context)))),

		// 3. inject the logger
		fx.Supply(s.log),

		// 4. use the logger also for fx' logs
		fx.WithLogger(func() fxevent.Logger {
			return &fxevent.ZapLogger{Logger: s.log.Named("fx")}
		}),

		// 5. provide user-provided options
		fx.Options(s.options...),

		// 5. provide user-provided run options
		fx.Options(options...),
	)
}
