package whatsapp

import (
	"context"
	"errors"
	"io"
)

type PairItem struct{ Event, Code string }
type PairingClient interface {
	Paired() bool
	Connect(context.Context) error
	Events() <-chan ConnectionEvent
	GetQRChannel(context.Context) (<-chan PairItem, error)
	IsLoggedIn() bool
}
type QRRenderer func(io.Writer, string) error

// Pair renders QR payloads only to an explicitly validated terminal writer.
func Pair(ctx context.Context, client PairingClient, session *SessionStore, output io.Writer, terminal bool, render QRRenderer) error {
	if client == nil || session == nil {
		return errors.New("WhatsApp pairing is unavailable")
	}
	if !client.Paired() && (!terminal || output == nil || render == nil) {
		return errors.New("WhatsApp QR pairing requires an interactive terminal")
	}
	var qr <-chan PairItem
	var err error
	if !client.Paired() {
		qr, err = client.GetQRChannel(ctx)
		if err != nil {
			return errors.New("could not initialize WhatsApp pairing")
		}
	}
	if err = client.Connect(ctx); err != nil {
		return errors.New("could not connect WhatsApp pairing session")
	}
	if client.Paired() {
		return awaitPairSuccess(ctx, client, session)
	}
	connected := false
	for qr != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-client.Events():
			if ok && event.Kind == ConnectionConnected {
				connected = true
			}
			if ok && event.Kind == ConnectionAuthenticationRequired {
				return errors.New("WhatsApp pairing requires reauthentication")
			}
		case item, ok := <-qr:
			if !ok {
				qr = nil
				continue
			}
			switch item.Event {
			case "code":
				if err = render(output, item.Code); err != nil {
					return errors.New("could not render WhatsApp pairing code")
				}
			case "success":
				if !connected {
					if err = awaitPairSuccess(ctx, client, session); err != nil {
						return err
					}
					return nil
				}
				return savePairedDevice(ctx, client, session)
			case "timeout":
				return errors.New("WhatsApp pairing QR expired")
			case "":
				return errors.New("WhatsApp pairing ended unexpectedly")
			default:
				return errors.New("WhatsApp pairing failed")
			}
		}
	}
	return errors.New("WhatsApp pairing ended without confirmation")
}

func awaitPairSuccess(ctx context.Context, client PairingClient, session *SessionStore) error {
	if client.IsLoggedIn() {
		return savePairedDevice(ctx, client, session)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("WhatsApp connection ended before pairing completed")
			}
			switch event.Kind {
			case ConnectionConnected:
				return savePairedDevice(ctx, client, session)
			case ConnectionAuthenticationRequired:
				return errors.New("WhatsApp pairing requires reauthentication")
			case ConnectionRestricted, ConnectionOutdated:
				return errors.New("WhatsApp pairing is temporarily unavailable")
			}
		}
	}
}
func savePairedDevice(ctx context.Context, client PairingClient, session *SessionStore) error {
	if !client.Paired() || !client.IsLoggedIn() {
		return errors.New("WhatsApp pairing was not confirmed")
	}
	if err := session.Device().Save(ctx); err != nil {
		return errors.New("could not persist WhatsApp pairing")
	}
	return nil
}

func (c *Client) GetQRChannel(ctx context.Context) (<-chan PairItem, error) {
	items, err := c.sdk.GetQRChannel(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan PairItem, 1)
	go func() {
		defer close(out)
		for item := range items {
			value := PairItem{Event: item.Event}
			if item.Event == "code" {
				value.Code = item.Code
			}
			select {
			case out <- value:
			case <-ctx.Done():
				return
			}
			if item.Event != "code" {
				return
			}
		}
	}()
	return out, nil
}
func (c *Client) IsLoggedIn() bool { return c.sdk != nil && c.sdk.IsLoggedIn() }
func (c *Client) Logout(ctx context.Context) error {
	if c.sdk == nil || !c.Paired() {
		return errors.New("WhatsApp session is not paired")
	}
	if err := c.sdk.Logout(ctx); err != nil {
		return errors.New("WhatsApp remote logout failed")
	}
	if err := c.session.Device().Delete(ctx); err != nil {
		return errors.New("could not clear WhatsApp session")
	}
	return nil
}
