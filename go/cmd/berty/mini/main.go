package mini

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell"
	"github.com/gdamore/tcell/terminfo"
	"github.com/gogo/protobuf/proto"
	"github.com/mr-tron/base58"
	"github.com/rivo/tview"
	"go.uber.org/zap"

	assets "berty.tech/berty/v2/go/pkg/assets"
	"berty.tech/berty/v2/go/pkg/errcode"
	"berty.tech/berty/v2/go/pkg/messengertypes"
	"berty.tech/weshnet/pkg/lifecycle"
	"berty.tech/weshnet/pkg/netmanager"
	"berty.tech/weshnet/pkg/protocoltypes"
)

type Opts struct {
	MessengerClient  messengertypes.MessengerServiceClient
	ProtocolClient   protocoltypes.ProtocolServiceClient
	Logger           *zap.Logger
	GroupInvitation  string
	DisplayName      string
	LifecycleManager *lifecycle.Manager
	NetManager       *netmanager.NetManager
}

var globalLogger *zap.Logger

func Main(ctx context.Context, opts *Opts) error {
	assets.Noop() // embed assets

	if opts.MessengerClient == nil {
		return errcode.ErrMissingInput.Wrap(fmt.Errorf("missing messenger client"))
	}
	if opts.ProtocolClient == nil {
		return errcode.ErrMissingInput.Wrap(fmt.Errorf("missing protocol client"))
	}
	_, err := terminfo.LookupTerminfo(os.Getenv("TERM"))
	if err != nil {
		return errcode.ErrCLINoTermcaps.Wrap(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	config, err := opts.ProtocolClient.ServiceGetConfiguration(ctx, &protocoltypes.ServiceGetConfiguration_Request{})
	if err != nil {
		return errcode.TODO.Wrap(err)
	}

	// In the first terminal:
	// cd berty/tool/berty-mini/local-helper
	// ID=1 make clean
	// ID=1 make run
	//
	// In the second terminal:
	// cd berty/tool/berty-mini/local-helper
	// ID=2 make clean
	// DAEMON_OPTS="-mini.group <share-uri>" ID=2 make run
	if len(opts.GroupInvitation) == 0 {
		doClient1(ctx, opts.ProtocolClient)
		return nil
	} else {
		doClient2(opts.GroupInvitation, ctx, opts.ProtocolClient)
		return nil
	}

	app := tview.NewApplication()

	accountGroup, err := opts.ProtocolClient.GroupInfo(ctx, &protocoltypes.GroupInfo_Request{
		GroupPK: config.AccountGroupPK,
	})
	if err != nil {
		return errcode.TODO.Wrap(err)
	}

	if opts.Logger != nil {
		globalLogger = opts.Logger.Named(pkAsShortID(accountGroup.Group.PublicKey))
	} else {
		globalLogger = zap.NewNop()
	}

	tabbedView := newTabbedGroups(ctx, accountGroup, opts.ProtocolClient, opts.MessengerClient, app, opts.DisplayName, opts.NetManager)
	if len(opts.GroupInvitation) > 0 {
		req := &protocoltypes.GroupMetadataList_Request{GroupPK: accountGroup.Group.PublicKey}
		cl, err := tabbedView.protocol.GroupMetadataList(ctx, req)
		if err != nil {
			return errcode.ErrEventListMetadata.Wrap(err)
		}

		go func() {
			for {
				evt, err := cl.Recv()
				switch err {
				case io.EOF: // gracefully ended @TODO: log this
					return
				case nil: // ok
				default:
					panic(err)
				}

				if evt.Metadata.EventType != protocoltypes.EventTypeAccountGroupJoined {
					continue
				}

				tabbedView.NextGroup()
			}
		}()

		for _, invit := range strings.Split(opts.GroupInvitation, ",") {
			if err := groupJoinCommand(ctx, tabbedView.accountGroupView, invit); err != nil {
				return errcode.TODO.Wrap(err)
			}
		}
	}

	input := tview.NewInputField().
		SetFieldTextColor(tcell.ColorWhite).
		SetFieldBackgroundColor(tcell.ColorBlack)

	input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			msg := input.GetText()
			input.SetText("")

			tabbedView.GetActiveViewGroup().OnSubmit(ctx, msg)
		}
	})

	inputBox := tview.NewFlex().
		AddItem(tview.NewTextView().SetText(">> "), 3, 0, false).
		AddItem(input, 0, 1, true)

	mainUI := tview.NewFlex().
		AddItem(tabbedView.GetTabs(), 10, 0, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(tabbedView.GetHistory(), 0, 1, false).
			AddItem(inputBox, 1, 1, true), 0, 1, true)

	// The inactive timer is disabled for now because it will cause group subs to be suspended
	// when going to inactive state
	// This will prevent desktop notification when inactive but they should not happen if subs
	// are suspended anyway

	/*
		const ShouldBecomeInactive = time.Second * 30
		inactiveTimer := time.AfterFunc(ShouldBecomeInactive, func() {
			opts.LifecycleManager.UpdateState(lifecycle.StateInactive)
		})
	*/

	keyboardCommandsMap := buildKeyboardCommandMap()

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		/*

			// reset timer
			if !inactiveTimer.Stop() {
				// AfterFunc timer should already have consume `inactiveTimer.C`
				opts.LifecycleManager.UpdateState(lifecycle.StateActive)
			}
			inactiveTimer.Reset(ShouldBecomeInactive)

		*/
		if _, ok := keyboardCommandsMap[event.Modifiers()]; ok {
			if action, ok := keyboardCommandsMap[event.Modifiers()][event.Key()]; ok {
				action(app, tabbedView, input)
				return nil
			}
		}

		return event
	})

	if err := app.SetRoot(mainUI, true).SetFocus(mainUI).Run(); err != nil {
		return errcode.TODO.Wrap(err)
	}

	return nil
}

func doClient1(ctx context.Context, client1 protocoltypes.ProtocolServiceClient) {
	// client1 shares contact with client2.
	binaryContact, err := shareContact(ctx, client1)
	if err != nil {
		panic(err)
	}
	fmt.Println("***Contact share:")
	fmt.Println(base58.Encode(binaryContact))

	// client1 receives the contact request from client2.
	request, err := receiveContactRequest(ctx, client1)
	if err != nil {
		panic(err)
	}
	if request == nil {
		fmt.Println("Error: Did not receive the contact request")
		return
	}

	// client1 accepts the contact request from client2.
	_, err = client1.ContactRequestAccept(ctx, &protocoltypes.ContactRequestAccept_Request{
		ContactPK: request.ContactPK,
	})
	if err != nil {
		panic(err)
	}

	// Activate the contact group.
	groupInfo, err := client1.GroupInfo(ctx, &protocoltypes.GroupInfo_Request{
		ContactPK: request.ContactPK,
	})
	if err != nil {
		panic(err)
	}
	_, err = client1.ActivateGroup(ctx, &protocoltypes.ActivateGroup_Request{
		GroupPK: groupInfo.Group.PublicKey,
	})
	if err != nil {
		panic(err)
	}

	// Receive a message from the group.
	message, err := receiveMessage(ctx, client1, groupInfo)
	if err != nil {
		panic(err)
	}
	if message == nil {
		fmt.Print("End of stream without receiving message")
		return
	}

	fmt.Println("client2:", string(message.Message))
}

func doClient2(encodedContact string, ctx context.Context, client2 protocoltypes.ProtocolServiceClient) {
	contact := &protocoltypes.ShareableContact{}
	contactBinary, err := base58.Decode(encodedContact)
	if err != nil {
		panic(err)
	}
	if err := proto.Unmarshal(contactBinary, contact); err != nil {
		panic(err)
	}

	// Send the contact request.
	_, err = client2.ContactRequestSend(ctx, &protocoltypes.ContactRequestSend_Request{
		Contact: contact,
	})
	if err != nil {
		panic(err)
	}

	// Activate the contact group.
	groupInfo, err := client2.GroupInfo(ctx, &protocoltypes.GroupInfo_Request{
		ContactPK: contact.PK,
	})
	if err != nil {
		panic(err)
	}
	_, err = client2.ActivateGroup(ctx, &protocoltypes.ActivateGroup_Request{
		GroupPK: groupInfo.Group.PublicKey,
	})
	if err != nil {
		panic(err)
	}

	// Send a message to the contact group.
	_, err = client2.AppMessageSend(ctx, &protocoltypes.AppMessageSend_Request{
		GroupPK: groupInfo.Group.PublicKey,
		Payload: []byte("Hello"),
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("Message sent. Sleep.")
	time.Sleep(time.Second * 2)
}

func shareContact(ctx context.Context, client protocoltypes.ProtocolServiceClient) ([]byte, error) {
	// We need the public rendezvous seed.
	contactRequestRef, err := client.ContactRequestReference(ctx,
		&protocoltypes.ContactRequestReference_Request{})
	if err != nil {
		return nil, err
	}
	if len(contactRequestRef.PublicRendezvousSeed) == 0 || !contactRequestRef.Enabled {
		// We need to reset the contact request reference and enable.
		_, err := client.ContactRequestResetReference(ctx,
			&protocoltypes.ContactRequestResetReference_Request{})
		if err != nil {
			return nil, err
		}
		_, err = client.ContactRequestEnable(ctx,
			&protocoltypes.ContactRequestEnable_Request{})
		if err != nil {
			return nil, err
		}

		// Refresh the info.
		contactRequestRef, err = client.ContactRequestReference(ctx,
			&protocoltypes.ContactRequestReference_Request{})
		if err != nil {
			return nil, err
		}
	}

	// Get the client's AccountPK from the configuration.
	config, err := client.ServiceGetConfiguration(ctx,
		&protocoltypes.ServiceGetConfiguration_Request{})
	if err != nil {
		return nil, err
	}
	contact := &protocoltypes.ShareableContact{
		PK:                   config.AccountPK,
		PublicRendezvousSeed: contactRequestRef.PublicRendezvousSeed,
	}
	return proto.Marshal(contact)
}

func receiveContactRequest(ctx context.Context, client protocoltypes.ProtocolServiceClient) (*protocoltypes.AccountContactRequestReceived, error) {
	// Get the client's AccountGroupPK from the configuration.
	config, err := client.ServiceGetConfiguration(ctx,
		&protocoltypes.ServiceGetConfiguration_Request{})
	if err != nil {
		return nil, err
	}

	// Subscribe to metadata events. ("sub" means "subscription".)
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	subMetadata, err := client.GroupMetadataList(subCtx, &protocoltypes.GroupMetadataList_Request{
		GroupPK: config.AccountGroupPK,
	})
	if err != nil {
		return nil, err
	}

	for {
		metadata, err := subMetadata.Recv()
		if err == io.EOF || subMetadata.Context().Err() != nil {
			// Not received.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}

		if metadata == nil || metadata.Metadata.EventType != protocoltypes.EventTypeAccountContactRequestIncomingReceived {
			continue
		}

		request := &protocoltypes.AccountContactRequestReceived{}
		if err = request.Unmarshal(metadata.Event); err != nil {
			return nil, err
		}

		return request, nil
	}
}

func receiveMessage(ctx context.Context, client protocoltypes.ProtocolServiceClient, groupInfo *protocoltypes.GroupInfo_Reply) (*protocoltypes.GroupMessageEvent, error) {
	// Subscribe to message events.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	subMessages, err := client.GroupMessageList(subCtx, &protocoltypes.GroupMessageList_Request{
		GroupPK: groupInfo.Group.PublicKey,
	})
	if err != nil {
		panic(err)
	}

	// client waits to receive the message.
	for {
		message, err := subMessages.Recv()
		if err == io.EOF {
			// Not received.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}

		return message, nil
	}
}
