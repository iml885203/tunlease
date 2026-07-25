package cliapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/iml885203/tunlease/pkg/tunnelclient"
)

const doctorTimeout = 5 * time.Second

func runDoctor(parent context.Context, client *tunnelclient.Client, path string, localPort int) error {
	ctx, cancel := context.WithTimeout(parent, doctorTimeout)
	defer cancel()

	problems := make([]error, 0, 2)
	if localPort != 0 {
		if localPort < 1 || localPort > 65535 {
			problems = append(problems, errors.New("local port must be between 1 and 65535"))
			fmt.Printf("✗ local service: invalid port %d\n", localPort)
		} else {
			address := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
			connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
			if err != nil {
				problems = append(problems, fmt.Errorf("local service %s: %w", address, err))
				fmt.Printf("✗ local service: cannot connect to %s\n", address)
			} else {
				_ = connection.Close()
				fmt.Printf("✓ local service: reachable at %s\n", address)
			}
		}
	} else {
		fmt.Println("– local service: skipped (pass --to PORT to check)")
	}

	claims, err := client.List(ctx)
	if err != nil {
		problems = append(problems, fmt.Errorf("gateway: %w", err))
		fmt.Printf("✗ gateway: %v\n", err)
	} else {
		fmt.Printf("✓ gateway: reachable and authenticated at %s\n", client.Gateway())
	}

	if path != "" {
		if err != nil {
			fmt.Printf("– path %s: skipped because gateway check failed\n", path)
		} else if owner, occupied := claimedBy(claims, path); occupied {
			problems = append(problems, fmt.Errorf("path %s overlaps an active claim owned by %s", path, owner))
			fmt.Printf("✗ path %s: overlaps an active claim owned by %s\n", path, owner)
		} else {
			fmt.Printf("✓ path %s: valid and not currently claimed\n", path)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("doctor found %d problem(s)", len(problems))
	}
	fmt.Println("ready to claim")
	return nil
}

func claimedBy(claims []tunnelclient.Claim, candidate string) (string, bool) {
	for _, claim := range claims {
		for _, active := range claim.Paths {
			if pathsOverlap(active, candidate) {
				return claim.Owner, true
			}
		}
	}
	return "", false
}

func pathsOverlap(left, right string) bool {
	left = strings.TrimSuffix(left, "*")
	right = strings.TrimSuffix(right, "*")
	return strings.HasPrefix(left, right) || strings.HasPrefix(right, left)
}
