package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// healthcheckCmd probes a local HTTP endpoint for container healthchecks.
// Distroless images have no shell or curl, so the binary checks itself.
func healthcheckCmd() *cobra.Command {
	var url string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Проверить HTTP-эндпоинт: код 0, если ответ 200",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return probe(cmd.Context(), url, timeout)
		},
	}
	cmd.Flags().StringVar(&url, "url", "http://127.0.0.1:8080/healthz", "адрес проверки")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "таймаут")
	return cmd
}

func probe(ctx context.Context, url string, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return nil
}
