package delivery_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"

	deliveryv1 "github.com/kantracity/kantrae2e/gen/kantra/delivery/v1"
	"github.com/kantracity/kantrae2e/gen/kantra/delivery/v1/deliveryv1connect"
	"github.com/kantracity/kantrae2e/internal/testutil"
	"github.com/kantracity/kantrae2e/pkg/authmiddleware"
	"github.com/kantracity/kantrae2e/services/delivery"
)

type env struct {
	t   *testing.T
	url string
	hc  *http.Client
}

type dev struct {
	id, user, tok string
	c             deliveryv1connect.DeliveryServiceClient
}

func setup(t *testing.T) *env {
	pool := testutil.Pool(t, testutil.DatabaseURL(t), delivery.Schema, delivery.Migrations())
	svc := delivery.New(pool, testutil.Secret, zerolog.Nop())
	path, h := svc.Handler()
	srv := testutil.Serve(t, map[string]http.Handler{path: h, delivery.WSPath: svc.WSHandler()})
	return &env{t: t, url: srv.URL, hc: srv.Client()}
}

func (e *env) device() *dev { return e.deviceOf(uuid.NewString()) }

func (e *env) deviceOf(userID string) *dev {
	id := uuid.NewString()
	tok, err := authmiddleware.Issue(testutil.Secret, userID, id, time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	c := deliveryv1connect.NewDeliveryServiceClient(e.hc, e.url, connect.WithInterceptors(
		connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
				r.Header().Set("Authorization", "Bearer "+tok)
				return next(ctx, r)
			}
		})))
	return &dev{id: id, user: userID, tok: tok, c: c}
}

func pending(t *testing.T, d *dev) []*deliveryv1.Envelope {
	t.Helper()
	r, err := d.c.FetchPending(context.Background(), &deliveryv1.FetchPendingRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return r.Envelopes
}

func ack(t *testing.T, d *dev, envs []*deliveryv1.Envelope) {
	t.Helper()
	var ids []int64
	for _, e := range envs {
		ids = append(ids, e.Id)
	}
	if _, err := d.c.Ack(context.Background(), &deliveryv1.AckRequest{Ids: ids}); err != nil {
		t.Fatal(err)
	}
}

func TestCommitOrderingAndFanOut(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, b, c := e.device(), e.device(), e.device()
	g := uuid.NewString()

	if _, err := a.c.CreateGroup(ctx, &deliveryv1.CreateGroupRequest{GroupId: g}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.CreateGroup(ctx, &deliveryv1.CreateGroupRequest{GroupId: g}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("dup group: %v", err)
	}
	// Non-members cannot commit.
	if _, err := b.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 0, Commit: []byte("x")}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-member commit: %v", err)
	}

	// Add b: epoch 0 -> 1, welcome to b.
	r, err := a.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{
		GroupId: g, Epoch: 0, Commit: []byte("commit-0"), Welcome: []byte("welcome-b"), AddedDeviceIds: []string{b.id}})
	if err != nil || r.NewEpoch != 1 {
		t.Fatalf("commit: %v %v", r, err)
	}
	pb := pending(t, b)
	if len(pb) != 1 || pb[0].Type != deliveryv1.MessageType_MESSAGE_TYPE_WELCOME || string(pb[0].Payload) != "welcome-b" || pb[0].Epoch != 1 {
		t.Fatalf("b pending: %v", pb)
	}
	ack(t, b, pb)
	w, err := b.c.FetchWelcome(ctx, &deliveryv1.FetchWelcomeRequest{GroupId: g})
	if err != nil || string(w.Welcome) != "welcome-b" {
		t.Fatalf("fetch welcome: %v", err)
	}

	// Stale commit is rejected with Aborted (HTTP 409).
	if _, err := b.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 0, Commit: []byte("late")}); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale commit: %v", err)
	}

	// Application messages: fan-out excludes the sender.
	if _, err := a.c.SendApplication(ctx, &deliveryv1.SendApplicationRequest{GroupId: g, Epoch: 1, Payload: []byte("ct")}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.SendApplication(ctx, &deliveryv1.SendApplicationRequest{GroupId: g, Epoch: 5, Payload: []byte("ct")}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("future epoch: %v", err)
	}
	if pa := pending(t, a); len(pa) != 0 {
		t.Fatalf("sender got own message: %v", pa)
	}
	pb = pending(t, b)
	if len(pb) != 1 || string(pb[0].Payload) != "ct" || pb[0].SenderDeviceId != a.id {
		t.Fatalf("b app: %v", pb)
	}
	ack(t, b, pb)

	// Add c, then remove c: c receives the removal commit but nothing after.
	if _, err := b.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 1, Commit: []byte("commit-1"),
		Welcome: []byte("welcome-c"), AddedDeviceIds: []string{c.id}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.SendApplication(ctx, &deliveryv1.SendApplicationRequest{GroupId: g, Epoch: 1, Payload: []byte("before-c")}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 2, Commit: []byte("commit-2"),
		RemovedDeviceIds: []string{c.id}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.SendApplication(ctx, &deliveryv1.SendApplicationRequest{GroupId: g, Epoch: 3, Payload: []byte("after")}); err != nil {
		t.Fatal(err)
	}
	// A message sent at an epoch before c joined is not routed to c.
	var got []string
	for _, env := range pending(t, c) {
		got = append(got, string(env.Payload))
	}
	if strings.Join(got, ",") != "welcome-c,commit-2" {
		t.Fatalf("c got %v", got)
	}
	gi, err := a.c.GetGroup(ctx, &deliveryv1.GetGroupRequest{GroupId: g})
	if err != nil || gi.CurrentEpoch != 3 || len(gi.MemberDeviceIds) != 2 {
		t.Fatalf("group: %v %v", gi, err)
	}
}

// Two (many) parallel commits for the same epoch: exactly one wins.
func TestConcurrentCommits(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	devs := []*dev{e.device()}
	g := uuid.NewString()
	if _, err := devs[0].c.CreateGroup(ctx, &deliveryv1.CreateGroupRequest{GroupId: g}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 7; i++ {
		d := e.device()
		devs = append(devs, d)
		ids = append(ids, d.id)
	}
	if _, err := devs[0].c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 0, Commit: []byte("c"),
		Welcome: []byte("w"), AddedDeviceIds: ids}); err != nil {
		t.Fatal(err)
	}

	for round := uint64(1); round <= 5; round++ {
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins, conflicts := 0, 0
		for _, d := range devs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := d.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: round, Commit: []byte(d.id)})
				mu.Lock()
				defer mu.Unlock()
				switch connect.CodeOf(err) {
				case connect.CodeAborted:
					conflicts++
				default:
					if err != nil {
						t.Errorf("commit: %v", err)
						return
					}
					wins++
				}
			}()
		}
		wg.Wait()
		if wins != 1 || conflicts != len(devs)-1 {
			t.Fatalf("round %d: wins=%d conflicts=%d", round, wins, conflicts)
		}
	}
	gi, _ := devs[0].c.GetGroup(ctx, &deliveryv1.GetGroupRequest{GroupId: g})
	if gi.CurrentEpoch != 6 {
		t.Fatalf("epoch %d", gi.CurrentEpoch)
	}
}

func TestWebSocketDeliveryAndOfflineQueue(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := e.device(), e.device()
	g := uuid.NewString()
	if _, err := a.c.CreateGroup(ctx, &deliveryv1.CreateGroupRequest{GroupId: g}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 0, Commit: []byte("c"),
		Welcome: []byte("w"), AddedDeviceIds: []string{b.id}}); err != nil {
		t.Fatal(err)
	}
	// b is offline; message is queued.
	if _, err := a.c.SendApplication(ctx, &deliveryv1.SendApplicationRequest{GroupId: g, Epoch: 1, Payload: []byte("offline")}); err != nil {
		t.Fatal(err)
	}

	dial := func() *websocket.Conn {
		wsURL := "wss" + strings.TrimPrefix(e.url, "https") + delivery.WSPath
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPClient: e.hc,
			HTTPHeader: http.Header{"Authorization": {"Bearer " + b.tok}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	read := func(conn *websocket.Conn) *deliveryv1.Envelope {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var env deliveryv1.Envelope
		if err := proto.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		return &env
	}
	sendAck := func(conn *websocket.Conn, id int64) {
		f, _ := proto.Marshal(&deliveryv1.ClientFrame{Frame: &deliveryv1.ClientFrame_Ack{Ack: &deliveryv1.Ack{Ids: []int64{id}}}})
		if err := conn.Write(ctx, websocket.MessageBinary, f); err != nil {
			t.Fatal(err)
		}
	}

	conn := dial()
	w := read(conn)
	off := read(conn)
	if w.Type != deliveryv1.MessageType_MESSAGE_TYPE_WELCOME || string(off.Payload) != "offline" {
		t.Fatalf("got %v %v", w, off)
	}
	sendAck(conn, w.Id)
	// Do not ack "offline": it must be re-delivered after reconnect.

	// Live push.
	if _, err := a.c.SendApplication(ctx, &deliveryv1.SendApplicationRequest{GroupId: g, Epoch: 1, Payload: []byte("live")}); err != nil {
		t.Fatal(err)
	}
	live := read(conn)
	if string(live.Payload) != "live" {
		t.Fatalf("live: %v", live)
	}
	sendAck(conn, live.Id)
	time.Sleep(100 * time.Millisecond) // let acks land
	conn.Close(websocket.StatusNormalClosure, "")

	conn = dial()
	defer conn.CloseNow()
	again := read(conn)
	if again.Id != off.Id {
		t.Fatalf("expected redelivery of %d, got %v", off.Id, again)
	}

	// Account-level tokens cannot open the stream.
	tok, _ := authmiddleware.Issue(testutil.Secret, uuid.NewString(), "", time.Hour)
	_, resp, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(e.url, "https")+delivery.WSPath, &websocket.DialOptions{
		HTTPClient: e.hc, HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("account token ws: %v", err)
	}
}

func TestMultiDevice(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	aliceUser := uuid.NewString()
	laptop, tablet, phone := e.deviceOf(aliceUser), e.deviceOf(aliceUser), e.deviceOf(aliceUser)
	bob, mallory := e.device(), e.device()
	g := uuid.NewString()

	if _, err := laptop.c.CreateGroup(ctx, &deliveryv1.CreateGroupRequest{GroupId: g, GroupInfo: []byte("gi-0")}); err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 0, Commit: []byte("add-bob"),
		Welcome: []byte("w"), AddedDeviceIds: []string{bob.id}, AddedUserIds: []string{bob.user}, GroupInfo: []byte("gi-1")}); err != nil {
		t.Fatal(err)
	}

	// Alice's tablet discovers the group and its current GroupInfo.
	mine, err := tablet.c.ListMyGroups(ctx, &deliveryv1.ListMyGroupsRequest{})
	if err != nil || len(mine.Groups) != 1 || mine.Groups[0].GroupId != g || mine.Groups[0].DeviceIsMember {
		t.Fatalf("list: %v %v", mine, err)
	}
	gi, err := tablet.c.GetGroupInfo(ctx, &deliveryv1.GetGroupInfoRequest{GroupId: g})
	if err != nil || string(gi.GroupInfo) != "gi-1" || gi.Epoch != 1 {
		t.Fatalf("group info: %v %v", gi, err)
	}
	// Other users can neither read the GroupInfo nor join by themselves.
	if _, err := mallory.c.GetGroupInfo(ctx, &deliveryv1.GetGroupInfoRequest{GroupId: g}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("mallory group info: %v", err)
	}
	if _, err := mallory.c.ExternalJoin(ctx, &deliveryv1.ExternalJoinRequest{GroupId: g, Epoch: 1, Commit: []byte("x"), GroupInfo: []byte("x")}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("mallory join: %v", err)
	}
	if _, err := mallory.c.RequestJoin(ctx, &deliveryv1.RequestJoinRequest{GroupId: g, KeyPackage: []byte("kp")}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("mallory request: %v", err)
	}

	// Stale epoch -> conflict; current epoch -> accepted.
	if _, err := tablet.c.ExternalJoin(ctx, &deliveryv1.ExternalJoinRequest{GroupId: g, Epoch: 0, Commit: []byte("ext"), GroupInfo: []byte("gi")}); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale join: %v", err)
	}
	r, err := tablet.c.ExternalJoin(ctx, &deliveryv1.ExternalJoinRequest{GroupId: g, Epoch: 1, Commit: []byte("ext-tablet"), GroupInfo: []byte("gi-2")})
	if err != nil || r.NewEpoch != 2 {
		t.Fatalf("join: %v %v", r, err)
	}
	var found bool
	for _, env := range pending(t, bob) {
		if string(env.Payload) == "ext-tablet" && env.SenderUserId == aliceUser && env.SenderDeviceId == tablet.id {
			found = true
		}
	}
	if !found {
		t.Fatal("bob did not get the external commit")
	}
	if _, err := tablet.c.ExternalJoin(ctx, &deliveryv1.ExternalJoinRequest{GroupId: g, Epoch: 2, Commit: []byte("again"), GroupInfo: []byte("gi")}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("double join: %v", err)
	}
	if mine, _ := tablet.c.ListMyGroups(ctx, &deliveryv1.ListMyGroupsRequest{}); !mine.Groups[0].DeviceIsMember {
		t.Fatal("tablet not a member")
	}

	// A commit without GroupInfo invalidates the stored one.
	if _, err := bob.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 2, Commit: []byte("upd")}); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.c.GetGroupInfo(ctx, &deliveryv1.GetGroupInfoRequest{GroupId: g}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("stale group info served: %v", err)
	}

	// Fallback: the phone asks members to add it.
	if _, err := phone.c.RequestJoin(ctx, &deliveryv1.RequestJoinRequest{GroupId: g, KeyPackage: []byte("kp-phone")}); err != nil {
		t.Fatal(err)
	}
	var req *deliveryv1.Envelope
	for _, env := range pending(t, laptop) {
		if env.Type == deliveryv1.MessageType_MESSAGE_TYPE_JOIN_REQUEST {
			req = env
		}
	}
	if req == nil || string(req.Payload) != "kp-phone" || req.SenderDeviceId != phone.id || req.SenderUserId != aliceUser {
		t.Fatalf("join request: %v", req)
	}

	// A removed device can neither see nor rejoin the group by itself.
	if _, err := bob.c.SendCommit(ctx, &deliveryv1.SendCommitRequest{GroupId: g, Epoch: 3, Commit: []byte("rm-tablet"),
		RemovedDeviceIds: []string{tablet.id}, GroupInfo: []byte("gi-4")}); err != nil {
		t.Fatal(err)
	}
	if mine, _ := tablet.c.ListMyGroups(ctx, &deliveryv1.ListMyGroupsRequest{}); len(mine.Groups) != 0 {
		t.Fatalf("banned device still lists group: %v", mine.Groups)
	}
	if _, err := tablet.c.ExternalJoin(ctx, &deliveryv1.ExternalJoinRequest{GroupId: g, Epoch: 4, Commit: []byte("back"), GroupInfo: []byte("gi")}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("banned rejoin: %v", err)
	}
}
