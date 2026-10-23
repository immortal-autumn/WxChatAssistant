// native-probe tests native desktop transport without logging in to an account.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kevmo314/wechat-cli/internal/native"
)

func main() {
	host := flag.String("host", "long.weixin.qq.com", "Native Tencent service hostname")
	profile := flag.String("profile", native.DesktopProfile, "desktop or experimental android-tablet")
	poll := flag.Bool("poll", false, "poll an unapproved QR once without authenticating or saving credentials")
	qr := flag.String("qr", "", "Attempt one native QR request and save its image (not a complete login)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	session, err := native.Connect(ctx, *host)
	if err != nil {
		fmt.Fprintln(os.Stderr, "native-probe:", err)
		os.Exit(1)
	}
	defer session.Close()
	fmt.Println(session)
	fmt.Println("Server signature, encrypted records, Finished, and NOOP verified; no account login or messages.")
	if *qr != "" {
		attempt, err := session.StartLoginWithProfile(ctx, *host, *profile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "native QR probe:", err)
			os.Exit(1)
		}
		if err = os.WriteFile(*qr, attempt.Image, 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Native QR image saved (%d bytes). This probe does not poll or complete login.\n", len(attempt.Image))
		if *poll {
			if err = session.PollLogin(ctx, attempt); err != nil {
				fmt.Fprintln(os.Stderr, "native poll probe:", err)
				os.Exit(1)
			}
			fmt.Printf("Native profile %s poll succeeded: status %d; no account authentication attempted.\n", attempt.Profile, attempt.Status)
		}
	}
}
