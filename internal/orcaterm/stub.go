// Package orcaterm is the terminal half of chat delivery (cli#907, C1).
// RED: signatures only; the implementation follows in the next commit.
package orcaterm

import (
	"context"
	"errors"
	"time"
)

type Result struct {
	Code   int
	Stdout string
	Stderr string
}

type Runner func(ctx context.Context, argv []string) Result

const (
	CallTimeout    = 300 * time.Second
	MinCallTimeout = time.Duration(0)
	SubmitWait     = 30
)

func Exec(bin string, timeout time.Duration) Runner {
	return func(context.Context, []string) Result { return Result{} }
}

func Guard(run Runner) Runner { return run }

type Terminal struct {
	Handle        string
	WorktreePath  string
	AgentIdentity *string
	Connected     bool
	Writable      bool
}

func (t Terminal) Identity() string { return "" }

var ErrOrca = errors.New("orca call failed")

type Client struct{ run Runner }

func NewClient(run Runner) *Client { return &Client{run: run} }

func (c *Client) List(ctx context.Context) ([]Terminal, error) { return nil, ErrOrca }

type Screen struct {
	Source string
	Tail   []string
	Draft  string
}

func (c *Client) ReadScreen(ctx context.Context, handle string) *Screen { return nil }

type Receipt struct {
	Accepted    bool
	TurnStarted bool
	Stages      []string
	ErrCode     string
}

type SendOptions struct {
	Interrupt bool
	EnterOnly bool
}

func (c *Client) Send(ctx context.Context, handle, text string, o SendOptions) Receipt { return Receipt{} }

func Normalize(s string) string { return s }

type Composer string

const (
	ComposerEmpty    Composer = "empty"
	ComposerOwn      Composer = "own-leftover"
	ComposerDraft    Composer = "draft"
	ComposerQuestion Composer = "question"
	ComposerUnknown  Composer = "screen-unknown"
)

func ReadComposer(sc *Screen, family, ownPrefix string) (Composer, string) { return "", "" }

type Liveness string

const (
	Busy    Liveness = "busy"
	Idle    Liveness = "idle"
	Unknown Liveness = "unknown"
)

func IdleProof(lines []string, family string) (Liveness, string) { return "", "" }

func ScreenAgentFamily(sc *Screen) string { return "x" }
