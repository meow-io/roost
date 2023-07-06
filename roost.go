package roost

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"sort"
	"strconv"
	"text/template"
	"time"

	"github.com/meow-io/go-slick"
	"github.com/meow-io/go-slick/config"
	"github.com/meow-io/go-slick/data/eav"
	"github.com/meow-io/go-slick/ids"
	"github.com/meow-io/go-slick/messaging"
	"github.com/meow-io/go-slick/migration"
	"github.com/rivo/uniseg"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
)

const (
	StateNew = iota
	StateLocked
	StateRunning

	UpdateAppState = iota
	UpdateGroupUpdate
	UpdateViewUpdate
	UpdateEntityUpdate
	UpdateIntroUpdate
	UpdateTransportStateUpdate
	UpdateMessagesFetched
	UpdateUnknown
	UpdateFinished

	MessagesPageSize = 20
)

func now() float64 {
	return float64(time.Now().UnixMicro()) / 1000000
}

func val(i interface{}) *eav.Value {
	return eav.NewValue(i)
}

func randomPosition(start, end float64) float64 {
	r := rand.Float64()*0.8 + 0.1 // #nosec G404
	return (end-start)*r + start
}

type keyMaker func(*Roost, string) ([]byte, error)

type ViewUpdate struct {
	viewName string
}

type EntityUpdate struct {
	viewName string
	GroupID  []byte
	EntityID []byte
}

const PageSize = 100

type Result struct {
	RowID     int64  `db:"rowid"`
	Text      string `db:"text"`
	EntityID  []byte `db:"entity_id"`
	GroupID   []byte `db:"group_id"`
	TopicID   []byte `db:"topic_id"`
	TopicName string `db:"topic_name"`
	Type      string `db:"type"`
}

type SearchResults struct {
	GroupID        []byte
	Term           string
	HighlightStart string
	HighlightEnd   string
	Offset         int
	Total          int
	Count          int
	results        []*Result
}

func (sr *SearchResults) Result(i int) *Result {
	return sr.results[i]
}

// Topic is a organizational structure within a group.
type Topic struct {
	ID                  []byte  `db:"id"`
	GroupID             []byte  `db:"group_id"`
	CtimeSec            float64 `db:"_ctime"`
	MtimeSec            float64 `db:"_mtime"`
	WtimeSec            float64 `db:"_wtime"`
	IdentityID          []byte  `db:"_identity_tag"`
	MembershipID        []byte  `db:"_membership_tag"`
	Label               string  `db:"label"`
	MessageLastRead     float64 `db:"message_last_read"`
	UnreadMessageCount  int     `db:"unread_message_count"`
	IncompleteTodoCount int     `db:"incomplete_todo_count"`
	UnreadTodoCount     int     `db:"unread_todo_count"`
	ShowCompleted       bool    `db:"show_completed"`
	Pinned              bool    `db:"pinned"`
	Position            float64 `db:"position"`
	PinPosition         float64 `db:"pin_position"`
}

// Message is a chat message sent within the context of a topic.
type Message struct {
	ID           []byte  `db:"id"`
	GroupID      []byte  `db:"group_id"`
	CtimeSec     float64 `db:"_ctime"`
	MtimeSec     float64 `db:"_mtime"`
	WtimeSec     float64 `db:"_wtime"`
	IdentityID   []byte  `db:"_identity_tag"`
	MembershipID []byte  `db:"_membership_tag"`
	TopicID      []byte  `db:"topic_id"`
	Body         string  `db:"body"`
}

// Reaction is a "rune" that refers to another entity (such as a message or todo item)
type Reaction struct {
	ID           []byte  `db:"id"`
	GroupID      []byte  `db:"group_id"`
	CtimeSec     float64 `db:"_ctime"`
	MtimeSec     float64 `db:"_mtime"`
	WtimeSec     float64 `db:"_wtime"`
	IdentityID   []byte  `db:"_identity_tag"`
	MembershipID []byte  `db:"_membership_tag"`
	Rune         string  `db:"rune"`
	Active       bool    `db:"active"`
	EntityID     []byte  `db:"entity_id"`
}

type Device struct {
	device *slick.Device
}

func (d *Device) Name() string {
	return d.device.Name
}

func (d *Device) Type() string {
	return d.device.Type
}

// A container for paged messages which indicates if there are further messages and provides
// a cursor for continued paging.
type PagedMessages struct {
	AtEnd  bool
	Cursor string
	Count  int

	values []*Message
}

func (pm *PagedMessages) Message(i int) *Message {
	return pm.values[i]
}

// Todo item within a topic.
type Todo struct {
	ID                []byte  `db:"id"`
	GroupID           []byte  `db:"group_id"`
	CtimeSec          float64 `db:"_ctime"`
	MtimeSec          float64 `db:"_mtime"`
	WtimeSec          float64 `db:"_wtime"`
	IdentityID        []byte  `db:"_identity_tag"`
	MembershipID      []byte  `db:"_membership_tag"`
	TopicID           []byte  `db:"topic_id"`
	Body              string  `db:"body"`
	CompletedAt       float64 `db:"completed_at"`
	CompletedPosition float64 `db:"completed_position"`
	Deleted           bool    `db:"deleted"`
	Read              bool    `db:"read"`
	Position          float64 `db:"position"`
}

func (t *Todo) Complete() bool {
	return t.CompletedAt == 0
}

type Devices struct {
	Count int

	devices []*slick.Device
}

func (d *Devices) Device(i int) *Device {
	return &Device{d.devices[i]}
}

type Groups struct {
	Count  int
	groups []*RoostGroup
}

func (g *Groups) Group(i int) *RoostGroup {
	return g.groups[i]
}

type Reactions struct {
	Count     int
	reactions []*Reaction
}

func (r *Reactions) Reaction(i int) *Reaction {
	return r.reactions[i]
}

type AppState struct {
	State int
}

type GroupUpdate struct {
	ID                   []byte
	AckedMemberCount     int
	GroupState           int
	MemberCount          int
	ConnectedMemberCount int
	Seq                  int64
	PendingMessageCount  int
}

type TableUpdate struct {
	ID        []byte
	Name      string
	Tablename string
}

type IntroUpdate struct {
	GroupID   []byte
	Initiator bool
	Stage     int
	Type      int
}

type TransportStateUpdate struct {
	URL   string
	State string
}

type TransportStates struct {
	urls   []string
	states []string
}

func makeTransportStates(m map[string]string) *TransportStates {
	keys := maps.Keys(m)
	sort.Strings(keys)
	values := make([]string, len(keys))
	for i, k := range keys {
		values[i] = m[k]
	}
	return &TransportStates{keys, values}
}

func (t *TransportStates) Len() int {
	return len(t.urls)
}

func (t *TransportStates) URL(i int) string {
	return t.urls[i]
}

func (t *TransportStates) State(i int) string {
	return t.states[i]
}

//		*AppState: an update about the current state of Roost
//	  *GroupUpdate: an update about a group
//	  *ViewUpdate: an update about a specific view within roost, for instance `todos`, `topics` or `messages`.
//	  *EntityUpdate: an update about a specific view row within roost, for instance `todos`, `topics` or `messages`.
//	  *IntroUpdate: an update about a specific table within roost, for instance `todos`, `topics` or `messages`.
//	  *StateUpdate: an update about a specific table within roost, for instance `todos`, `topics` or `messages`.
type Updates struct {
	updates chan interface{}
	item    interface{}
}

func (u *Updates) Next() {
	u.item = <-u.updates
}

func (u *Updates) Type() int {
	if u.item == nil {
		return UpdateFinished
	}

	switch u.item.(type) {
	case *slick.AppState:
		return UpdateAppState
	case *slick.GroupUpdate:
		return UpdateGroupUpdate
	case *ViewUpdate:
		return UpdateViewUpdate
	case *EntityUpdate:
		return UpdateEntityUpdate
	case *slick.IntroUpdate:
		return UpdateIntroUpdate
	case *slick.TransportStateUpdate:
		return UpdateTransportStateUpdate
	case *slick.MessagesFetchedUpdate:
		return UpdateMessagesFetched
	default:
		fmt.Printf("unknown event type is %T\n", u.item)
		return UpdateUnknown
	}
}

func (u *Updates) AppState() *AppState {
	return &AppState{u.item.(*slick.AppState).State}
}

func (u *Updates) GroupUpdate() *GroupUpdate {
	gu := u.item.(*slick.GroupUpdate)
	return &GroupUpdate{
		ID:                   gu.ID[:],
		AckedMemberCount:     int(gu.AckedMemberCount),
		GroupState:           gu.GroupState,
		MemberCount:          int(gu.MemberCount),
		ConnectedMemberCount: int(gu.ConnectedMemberCount),
		Seq:                  int64(gu.Seq),
		PendingMessageCount:  int(gu.PendingMessageCount),
	}
}

func (u *Updates) ViewUpdate() *ViewUpdate {
	return u.item.(*ViewUpdate)
}

func (u *Updates) TransportStateUpdate() *TransportStateUpdate {
	su := u.item.(*slick.TransportStateUpdate)
	return &TransportStateUpdate{
		URL:   su.URL,
		State: su.State,
	}
}

func (u *Updates) ViewEntityUpdate() *EntityUpdate {
	return u.item.(*EntityUpdate)
}

func (u *Updates) IntroUpdate() *IntroUpdate {
	tu := u.item.(*slick.IntroUpdate)
	return &IntroUpdate{
		GroupID:   tu.GroupID[:],
		Initiator: tu.Initiator,
		Stage:     int(tu.Stage),
		Type:      int(tu.Type),
	}
}

type Todos struct {
	IncompleteCount int
	CompleteCount   int
	incompleteTodos []*Todo
	completeTodos   []*Todo
}

func (t *Todos) CompleteTodo(i int) *Todo {
	return t.completeTodos[i]
}
func (t *Todos) IncompleteTodo(i int) *Todo {
	return t.incompleteTodos[i]
}

type TodoUpdater struct {
	rg        *RoostGroup
	completed map[ids.ID]bool
	read      map[ids.ID]bool
}

func (tu *TodoUpdater) MarkComplete(id []byte, complete bool) {
	tu.completed[ids.IDFromBytes(id)] = complete
}

func (tu *TodoUpdater) MarkRead(id []byte, read bool) {
	tu.read[ids.IDFromBytes(id)] = read
}

// also give them new positions when you complete them
func (tu *TodoUpdater) Commit() error {
	nowTs := now()
	writer := tu.rg.roost.slick.EAVWriter(tu.rg.group)

	for k, v := range tu.completed {
		k := k
		if v {
			writer.Update("todos", k[:], map[string]interface{}{
				"completed_at":       nowTs,
				"completed_position": -nowTs,
			})
		} else {
			writer.Update("todos", k[:], map[string]interface{}{
				"completed_at":       float64(0),
				"completed_position": float64(0),
			})
		}
	}
	for k, v := range tu.read {
		k := k
		writer.Update("todos", k[:], map[string]interface{}{
			"read": v,
		})
	}
	if err := writer.Execute(); err != nil {
		return err
	}
	tu.completed = make(map[ids.ID]bool)
	tu.read = make(map[ids.ID]bool)
	return nil
}

type Topics struct {
	Count int

	pinnedTopics   []*Topic
	unpinnedTopics []*Topic
}

func (t *Topics) Topic(i int) *Topic {
	if i < len(t.pinnedTopics) {
		return t.pinnedTopics[i]
	}
	return t.unpinnedTopics[i+len(t.pinnedTopics)]
}

type Roost struct {
	log           *zap.SugaredLogger
	slick         *slick.Slick
	State         int
	keyMaker      keyMaker
	heyaAuthToken string
	updates       chan interface{}
}

// Makes a Roost instance with a given key maker. Not typically used outside of tests.
func MakeRoostWithKeyMaker(root, heyaAuthToken string, keyMaker keyMaker) (*Roost, error) {
	c := config.NewConfig(config.WithLoggingPrefix(root), config.WithRootDir(root))
	log := c.Logger("roost")

	updates := make(chan interface{}, 100)
	s, err := slick.NewSlick(c, func(s *slick.Slick) error {
		err := s.DB.Migrate("roost", []*migration.Migration{
			{
				Name: "Create initial tables",
				Func: func(tx *sql.Tx) error {
					if err := s.EAVCreateViews(map[string]*eav.ViewDefinition{
						"messages": {
							Columns: map[string]*eav.ColumnDefinition{
								"body": {
									SourceName: "message_body",
									ColumnType: eav.Text,
									Required:   true,
									Nullable:   false,
								},
								"topic_id": {
									SourceName: "message_topic_id",
									ColumnType: eav.Blob,
									Required:   true,
									Nullable:   false,
								},
							},
							Indexes: [][]string{{"_ctime"}, {"group_id", "topic_id"}},
						},
						"todos": {
							Columns: map[string]*eav.ColumnDefinition{
								"body": {
									SourceName: "todo_body",
									ColumnType: eav.Text,
									Required:   true,
									Nullable:   false,
								},
								"topic_id": {
									SourceName: "todo_topic_id",
									ColumnType: eav.Blob,
									Required:   true,
									Nullable:   false,
								},
								"read": {
									SourceName:   "_self_todo_read",
									ColumnType:   eav.Int,
									DefaultValue: val(0),
									Required:     false,
									Nullable:     false,
								},
								"completed_at": {
									SourceName:   "todo_completed_at",
									ColumnType:   eav.Real,
									DefaultValue: val(0),
									Required:     false,
									Nullable:     false,
								},
								"deleted": {
									SourceName:   "todo_deleted",
									ColumnType:   eav.Int,
									DefaultValue: val(0),
									Required:     false,
									Nullable:     false,
								},
								"position": {
									SourceName:   "todo_position",
									ColumnType:   eav.Real,
									DefaultValue: val(0),
									Required:     false,
									Nullable:     false,
								},
								"completed_position": {
									SourceName:   "todo_completed_position",
									ColumnType:   eav.Real,
									DefaultValue: val(0),
									Required:     false,
									Nullable:     false,
								},
							},
							Indexes: [][]string{{"_ctime"}, {"group_id", "topic_id", "read"}},
						},
						"topics": {
							Columns: map[string]*eav.ColumnDefinition{
								"label": {
									SourceName: "topic_label",
									ColumnType: eav.Text,
									Required:   true,
									Nullable:   false,
								},
								"message_last_read": {
									SourceName:   "_self_topic_message_last_read",
									ColumnType:   eav.Real,
									DefaultValue: val(float64(0)),
									Required:     false,
									Nullable:     false,
								},
								"show_completed": {
									SourceName:   "_self_topic_show_completed",
									ColumnType:   eav.Int,
									DefaultValue: val(0),
									Required:     false,
									Nullable:     false,
								},
								"position": {
									SourceName:   "_self_topic_position",
									ColumnType:   eav.Real,
									Nullable:     false,
									DefaultValue: val(0),
								},
								"pin_position": {
									SourceName:   "_self_topic_pin_position",
									ColumnType:   eav.Real,
									Nullable:     false,
									DefaultValue: val(0),
								},
								"pinned": {
									SourceName:   "_self_topic_pinned",
									ColumnType:   eav.Int,
									Nullable:     false,
									DefaultValue: val(0),
								},
							},
							Indexes: [][]string{{"_ctime"}, {"group_id"}},
						},
						"reactions": {
							Columns: map[string]*eav.ColumnDefinition{
								"active": {
									SourceName:   "reaction_active",
									ColumnType:   eav.Int,
									Nullable:     false,
									DefaultValue: val(1),
								},
								"entity_id": {
									SourceName: "reaction_entity_id",
									ColumnType: eav.Blob,
									Required:   true,
									Nullable:   false,
								},
								"rune": {
									SourceName: "reaction_rune",
									ColumnType: eav.Text,
									Required:   true,
									Nullable:   false,
								},
							},
							Indexes: [][]string{
								{"group_id", "entity_id"},
							},
						},
					}); err != nil {
						return err
					}

					statementTmpl, err := template.New("index_statement").
						Funcs(template.FuncMap{
							"index_where": func(viewName, prefix string) (string, error) {
								return s.EAVIndexWhere(viewName, prefix)
							},
							"selectors": func(viewName, prefix string, colName ...string) (string, error) {
								return s.EAVSelectors(viewName, prefix, colName...)
							},
						}).Parse(`
					CREATE TABLE fs_contents(
					id INTEGER PRIMARY KEY,
					group_id BINARY NOT NULL,
					topic_id BINARY NOT NULL,
					entity_id BINARY NOT NULL,
					type STRING NOT NULL,
					text STRING NOT NULL
					);
					CREATE UNIQUE INDEX content_group_entity on fs_contents(group_id, entity_id);

					CREATE VIRTUAL TABLE fs_content_fts_idx USING fts5(text, content='fs_contents', content_rowid='id');

					CREATE TRIGGER fs_content_fts_idx_ai AFTER INSERT ON fs_contents BEGIN
					INSERT INTO fs_content_fts_idx(rowid, text) VALUES (new.id, new.text);
					END;
					CREATE TRIGGER fs_content_fts_idx_ad AFTER DELETE ON fs_contents BEGIN
					INSERT INTO fs_content_fts_idx(fs_content_fts_idx, rowid, text) VALUES('delete', old.id, old.text);
					END;
					CREATE TRIGGER fs_content_fts_idx_au AFTER UPDATE ON fs_contents BEGIN
					INSERT INTO fs_content_fts_idx('fs_content_fts_idx', rowid, text) VALUES('delete', old.id, old.text);
					INSERT INTO fs_content_fts_idx(rowid, text) VALUES(new.id, new.text);
					END;

					insert into fs_contents (group_id, topic_id, entity_id, type, text) select group_id, topic_id, id, 'todo', body from todos;
					insert into fs_contents (group_id, topic_id, entity_id, type, text) select group_id, topic_id, id, 'message', body from messages;

					-- Trigger for ft indexing
					CREATE TRIGGER todos_insert_contents AFTER INSERT ON _eav_data
					WHEN ({{ index_where "todos"  "new." }})
					BEGIN
					INSERT INTO fs_contents
						(group_id, topic_id, entity_id, text, type) VALUES
						({{ selectors "todos" "new." "group_id" "topic_id" "id" "body" }}, 'todo');
					END;
					CREATE TRIGGER messages_insert_contents AFTER INSERT ON _eav_data
					WHEN ({{ index_where "messages" "new." }})
					BEGIN
					INSERT INTO fs_contents
						(group_id, topic_id, entity_id, text, type) VALUES
						({{ selectors "messages" "new." "group_id" "topic_id" "id" "body" }}, 'message');
					END;

					CREATE TRIGGER eav_data_delete_contents AFTER DELETE ON _eav_data
					WHEN ({{ index_where "todos" "old." }}) OR ({{ index_where "messages" "old." }})
					BEGIN
					DELETE FROM fs_contents WHERE entity_id = old.id AND group_id = old.group_id;
					END;

					CREATE TRIGGER todos_update_contents AFTER UPDATE ON _eav_data
					WHEN ({{ index_where "todos"  "new." }})
					BEGIN

					UPDATE fs_contents set
						text={{ selectors "todos" "new." "body"}},
						topic_id = {{ selectors "todos" "new." "topic_id"}}
					where
						group_id = {{ selectors "todos" "new." "group_id"}} and entity_id = {{ selectors "todos" "new." "id"}};
					END;

					CREATE TRIGGER messages_update_contents AFTER UPDATE ON _eav_data
					WHEN ({{ index_where "messages" "new." }})
					BEGIN
					UPDATE fs_contents set
						text={{ selectors "messages" "new." "body"}},
						topic_id = {{ selectors "messages" "new." "topic_id"}}
					where
						group_id = {{ selectors "messages" "new." "group_id"}} and entity_id = {{ selectors "messages" "new." "id"}};
					END;
					`)
					if err != nil {
						return err
					}
					var t bytes.Buffer
					if err := statementTmpl.Execute(&t, nil); err != nil {
						return err
					}
					sql := t.String()
					if _, err := tx.Exec(sql); err != nil {
						return err
					}
					return nil
				},
			},
		})
		if err != nil {
			return err
		}
		s.EAVSubscribeAfterEntity(func(viewName string, groupID, id ids.ID) {
			updates <- &EntityUpdate{viewName, groupID[:], id[:]}
		}, false, "todos", "messages", "topics")

		s.EAVSubscribeAfterView(func(viewName string) {
			updates <- &ViewUpdate{viewName}
		}, false, "todos", "messages", "topics")

		return nil
	})
	if err != nil {
		return nil, err
	}
	u := s.Updates()
	go func() {
		for i := range u {
			updates <- i
		}
	}()

	state := StateNew
	if s.Initialized() {
		state = StateLocked
	}

	return &Roost{log, s, state, keyMaker, heyaAuthToken, updates}, nil
}

// Makes a Roost instance for a given root directory.
func MakeRoost(root, heyaAuthToken string) (*Roost, error) {
	return MakeRoostWithKeyMaker(root, heyaAuthToken, func(r *Roost, p string) ([]byte, error) {
		return r.slick.NewKey(p)
	})
}

// Makes a Roost instance for a given root directory.
func MakeRoostWithStrongKey(root, heyaAuthToken string) (*Roost, error) {
	return MakeRoostWithKeyMaker(root, heyaAuthToken, func(r *Roost, p string) ([]byte, error) {
		return []byte(p), nil
	})
}

// Get current transport states
func (r *Roost) TransportStates() *TransportStates {
	return makeTransportStates(r.slick.TransportStates())
}

// Generates a random 6-digit pin
func (r *Roost) NewPin() (string, error) {
	return slick.NewPin()
}

// Get the underlying Slick instance
func (r *Roost) Slick() *slick.Slick {
	return r.slick
}

// Gets a device link which can be used to link a device to the current device group.
func (r *Roost) GetDeviceLink() (string, error) {
	link, err := r.slick.DeviceGroup.GetDeviceLink()
	if err != nil {
		return "", err
	}
	return slick.SerializeDeviceInvite(link)
}

// Links a device using the provided device link.
func (r *Roost) LinkDevice(l string) error {
	link, err := slick.DeserializeDeviceInvite(l)
	if err != nil {
		return err
	}
	return r.slick.DeviceGroup.LinkDevice(link)
}

// Accepts an invite.
func (r *Roost) AcceptInvite(inviteURL string, password string) ([]byte, error) {
	parsed, err := url.Parse(inviteURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "roost" {
		return nil, fmt.Errorf("expected scheme roost, got %s", parsed.Scheme)
	}
	if parsed.Host != "invite" {
		return nil, fmt.Errorf("expected host to be 'invite', got %s", parsed.Host)
	}
	invite, err := slick.DeserializeInvite(parsed.Path[1:])
	if err != nil {
		return nil, err
	}
	id, err := r.slick.AcceptInvite(invite, password)
	return id[:], err
}

// Creates a roost group (a "roost")
func (r *Roost) CreateGroup(name string) (*RoostGroup, error) {
	groupID, err := r.slick.CreateGroup(name)
	if err != nil {
		return nil, err
	}
	group, err := makeRoostGroup(r, groupID[:])
	if err != nil {
		return nil, err
	}
	if _, err := group.CreateTopic("home"); err != nil {
		return nil, err
	}

	return group, nil
}

// Gets a group for a specific id.
func (r *Roost) Group(groupID []byte) (*RoostGroup, error) {
	return makeRoostGroup(r, groupID)
}

// Gets a list of available groups.
func (r *Roost) Groups() (*Groups, error) {
	groups, err := r.slick.Groups()

	if err != nil {
		return nil, err
	}

	roostGroups := make([]*RoostGroup, 0, len(groups))
	for _, group := range groups {
		if group.State != messaging.GroupStateSynced {
			continue
		}
		roostGroup, err := r.Group(group.ID[:])

		if err != nil {
			return nil, err
		}
		roostGroups = append(roostGroups, roostGroup)
	}
	return &Groups{len(roostGroups), roostGroups}, nil
}

// Gets messages. Clients should wait for an UpdateMessagesFetched event and shutdown after that.
func (r *Roost) GetMessages(password string) error {
	key, err := r.keyMaker(r, password)
	if err != nil {
		return err
	}
	return r.slick.GetMessages(key)
}

// Initializes roost with a given password.
func (r *Roost) Initialize(password string) error {
	key, err := r.keyMaker(r, password)
	if err != nil {
		return err
	}
	if err := r.slick.Initialize(key); err != nil {
		return err
	}
	if r.heyaAuthToken != "" {
		if err := r.RegisterHeyaTransport(r.heyaAuthToken, "heya.meow.io", 8337); err != nil {
			return err
		}
	}
	return r.updateState()
}

// Unlocks an already initialized roost with a given password.
func (r *Roost) Unlock(password string) error {
	key, err := r.keyMaker(r, password)
	if err != nil {
		return err
	}
	if err := r.slick.Open(key); err != nil {
		return err
	}
	return r.updateState()
}

// Shuts down an existing roost instance.
func (r *Roost) Shutdown() error {
	r.State = StateLocked
	return r.slick.Shutdown()
}

// Sets the current device name and type for a given roost instance.
func (r *Roost) SetDeviceNameType(name, ty string) error {
	return r.slick.DeviceGroup.SetNameType(name, ty)
}

// Gets a list of all connected devices to the current identity.
func (r *Roost) Devices() (*Devices, error) {
	d, err := r.slick.DeviceGroup.Devices()
	if err != nil {
		return nil, err
	}
	return &Devices{len(d), d}, nil
}

// Registers the HEYA transport which is the main transport used currently for Roost. This transport
// supports the sending of iOS push notifications.
func (r *Roost) RegisterHeyaTransport(authToken, host string, port int) error {
	return r.slick.RegisterHeyaTransport(authToken, host, port)
}

// Gets a channel of events which can occur within the application. For example application state changes
// and updates to specific tables within the EAV database.
//
// This channel will provide the following types:
//
//		*AppState: an update about the current state of Roost
//	  *GroupUpdate: an update about a group
//	  *TableUpdate: an update about a specific table within roost, for instance `todos`, `topics` or `messages`.
func (r *Roost) Updates() *Updates {
	return &Updates{r.updates, nil}
}

// Return the number of unread messages across all topics and groups
func (r *Roost) UnreadMessageCount() (int64, error) {
	var unreadCount int64
	if err := r.slick.EAVGet(&unreadCount, `select sum(unread_count) from (
		select count(*) as unread_count from messages m inner join topics t on m.group_id = t.group_id and m.topic_id = t.id where m._ctime > t.message_last_read
	)`); err != nil {
		return 0, err
	}
	return unreadCount, nil
}

// Leave a device group and destroy all Roost data on this device, returning it to a "new" state.
func (r *Roost) LeaveDeviceGroup() error {
	// TODO: still needs to be implemented
	return nil
}

// Adds a push notification token to Roost.
func (r *Roost) AddPushToken(token string) error {
	return r.slick.AddPushNotificationToken(token)
}

// Removes a push notification token from Roost.
func (r *Roost) DeletePushToken(token string) error {
	return r.slick.DeletePushNotificationToken(token)
}

func (r *Roost) updateState() error {
	if r.slick.New() {
		r.State = StateNew
		return nil
	} else if r.slick.Initialized() {
		r.State = StateLocked
		return nil
	} else if r.slick.Running() {
		r.State = StateRunning
		return nil
	} else {
		return errors.New("unknown state")
	}
}

// Perform a fulltext search across all your Roosts.
func (r *Roost) Search(groupID []byte, term, highlightStart, highlightEnd string) (*SearchResults, error) {
	var results *SearchResults
	return results, r.slick.DB.Run("search", func() error {
		var err error
		resultList, err := r.generateResultsGroup(groupID, term, highlightStart, highlightEnd, 0)
		if err != nil {
			return err
		}
		total, err := r.generateTotal(term)
		if err != nil {
			return err
		}
		results = &SearchResults{
			GroupID:        groupID,
			Term:           term,
			Offset:         0,
			HighlightStart: highlightStart,
			HighlightEnd:   highlightEnd,
			Total:          total,
			Count:          len(resultList),
			results:        resultList,
		}
		return nil
	})
}

// Fetch the next page of results from a previous search.
func (r *Roost) NextPage(results *SearchResults) (*SearchResults, error) {
	newOffset := results.Offset + PageSize
	resultList, err := r.generateResultsGroup(results.GroupID, results.Term, results.HighlightStart, results.HighlightEnd, newOffset)
	if err != nil {
		return nil, err
	}
	results.results = resultList
	results.Count = len(resultList)
	results.Offset = newOffset
	return results, nil
}

func (r *Roost) generateResultsGroup(groupID []byte, term, highlightStart, highlightEnd string, offset int) ([]*Result, error) {
	results := make([]*Result, 0)
	if err := r.slick.DB.Tx.Select(&results, `
	SELECT
		c.id as rowid,
		snippet(fs_content_fts_idx, 0, ?, ?, '…', 15) as text,
		c.entity_id as entity_id,
		c.group_id as group_id,
		c.topic_id as topic_id,
		t.label as topic_name,
		c.type as type
	FROM fs_content_fts_idx
	LEFT JOIN fs_contents c ON c.rowid = fs_content_fts_idx.rowid
	LEFT JOIN topics t ON t.group_id = c.group_id AND t.id = c.topic_id
	WHERE c.group_id = ? AND fs_content_fts_idx.text MATCH ?
	ORDER BY bm25(fs_content_fts_idx)
	LIMIT ? OFFSET ?`, highlightStart, highlightEnd, groupID, term, PageSize, offset); err != nil {
		return nil, err
	}

	return results, nil
}

func (r *Roost) generateTotal(term string) (int, error) {
	var count int
	if err := r.slick.DB.Tx.Get(&count, "select count(rowid) FROM fs_content_fts_idx WHERE text MATCH ?", term); err != nil {
		return 0, err
	}
	r.log.Debugf("generating count for %s total is %d", term, count)
	return count, nil
}

// A group (or "roost") within Roost. This represents a set of identities
// collaborating together in the same database.
type RoostGroup struct { //nolint:revive
	GroupID             []byte
	IdentityTag         []byte
	Name                string
	UnreadMessageCount  int
	IncompleteTodoCount int
	UnreadTodoCount     int

	roost *Roost
	group *slick.Group
}

func makeRoostGroup(roost *Roost, groupIDSlice []byte) (*RoostGroup, error) {
	groupID := ids.IDFromBytes(groupIDSlice)
	group, err := roost.slick.Group(groupID)
	if err != nil {
		return nil, err
	}

	g := &RoostGroup{groupID[:], group.IdentityTag[:], group.Name, 0, 0, 0, roost, group}
	topics, err := g.Topics()
	if err != nil {
		return nil, err
	}
	for i := 0; i != topics.Count; i++ {
		g.UnreadMessageCount += topics.Topic(i).UnreadMessageCount
		g.IncompleteTodoCount += topics.Topic(i).IncompleteTodoCount
		g.UnreadTodoCount += topics.Topic(i).UnreadTodoCount
	}

	return g, nil
}

// CreateTopic creates a new topic with the given name.
func (rg *RoostGroup) CreateTopic(label string) (*Topic, error) {
	return rg.CreateTopicPinned(label, false)
}

func (rg *RoostGroup) CreateTopicPinned(label string, pinned bool) (*Topic, error) {
	maxPosition := float64(0)
	maxPositionRow := struct {
		MaxPosition *float64 `db:"max_position"`
	}{}
	var statement string
	if pinned {
		statement = "select max(pin_position) as max_position from topics where group_id = ? AND pinned != 0"
	} else {
		statement = "select max(position) as max_position from topics where group_id = ? AND pinned = 0"
	}
	if err := rg.roost.slick.EAVGet(&maxPositionRow, statement, rg.group.ID[:]); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	} else if maxPositionRow.MaxPosition != nil {
		maxPosition = randomPosition(*maxPositionRow.MaxPosition, *maxPositionRow.MaxPosition+2)
	}

	return rg.createTopicPinned(label, pinned, maxPosition)
}

func (rg *RoostGroup) createTopicPinned(label string, pinned bool, position float64) (*Topic, error) {
	var positionProp string
	if pinned {
		positionProp = "pin_position"
	} else {
		positionProp = "position"
	}
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Insert("topics", map[string]interface{}{
		"label":      label,
		"pinned":     pinned,
		positionProp: position,
	})
	if err := writer.Execute(); err != nil {
		return nil, err
	}

	return rg.Topic(writer.InsertIDs[0][:])
}

// Creates a password-protected invite.
func (rg *RoostGroup) Invite(password string) (string, error) {
	invite, err := rg.roost.slick.Invite(rg.group.ID, password)
	if err != nil {
		return "", err
	}
	s, err := slick.SerializeInvite(invite)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("roost://invite/%s", s), nil
}

// Cancels invites to this group.
func (rg *RoostGroup) CancelInvites() error {
	return rg.roost.slick.CancelInvites(rg.group.ID)
}

// Updates a topic in a group.
func (rg *RoostGroup) UpdateTopic(topic *Topic) error {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("topics", topic.ID, map[string]interface{}{
		"label":             topic.Label,
		"message_last_read": topic.MessageLastRead,
	})
	return writer.Execute()
}

// Mark a topic as read
func (rg *RoostGroup) MarkTopicRead(topicID []byte) error {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("topics", topicID, map[string]interface{}{
		"message_last_read": now(),
	})
	return writer.Execute()
}

// Gets a topic for a given id.
func (rg *RoostGroup) Topic(id []byte) (*Topic, error) {
	topic := Topic{}
	if err := rg.roost.slick.EAVGet(&topic, "select *, (select count(id) from messages where messages.group_id = topics.group_id AND messages.topic_id = topics.id and messages._identity_tag != ? and messages._ctime > topics.message_last_read) as unread_message_count, (select count(id) from todos where todos.group_id = topics.group_id AND  topic_id = topics.id and todos.deleted = 0 and todos.completed_at = 0) as incomplete_todo_count, (select count(id) from todos where todos.group_id = topics.group_id AND  topic_id = topics.id and todos.deleted = 0 and todos.read = 0) as unread_todo_count from topics where group_id = ? AND id = ?", rg.group.IdentityTag[:], rg.group.ID[:], id[:]); err != nil {
		return nil, err
	}
	return &topic, nil
}

// Gets a list of all topics.
func (rg *RoostGroup) Topics() (*Topics, error) {
	var unpinnedTopics, pinnedTopics []*Topic
	if err := rg.roost.slick.EAVSelect(&pinnedTopics, "select *, (select count(id) from messages where messages.group_id = topics.group_id AND messages.topic_id = topics.id and messages._identity_tag != ? and messages._ctime > topics.message_last_read) as unread_message_count, (select count(id) from todos where todos.group_id = topics.group_id AND topic_id = topics.id and todos.deleted = 0 and todos.completed_at = 0) as incomplete_todo_count, (select count(id) from todos where todos.group_id = topics.group_id AND  topic_id = topics.id and todos.deleted = 0 and todos.read = 0) as unread_todo_count from topics WHERE group_id = ? AND pinned != 0 order by pin_position, _ctime", rg.group.IdentityTag[:], rg.group.ID[:]); err != nil {
		return nil, err
	}
	if err := rg.roost.slick.EAVSelect(&unpinnedTopics, "select *, (select count(id) from messages where messages.group_id = topics.group_id AND messages.topic_id = topics.id and messages._identity_tag != ? and messages._ctime > topics.message_last_read) as unread_message_count, (select count(id) from todos where todos.group_id = topics.group_id AND topic_id = topics.id and todos.deleted = 0 and todos.completed_at = 0) as incomplete_todo_count, (select count(id) from todos where todos.group_id = topics.group_id AND  topic_id = topics.id and todos.deleted = 0 and todos.read = 0) as unread_todo_count from topics WHERE group_id = ? AND pinned = 0 order by position, _ctime", rg.group.IdentityTag[:], rg.group.ID[:]); err != nil {
		return nil, err
	}
	return &Topics{len(pinnedTopics) + len(unpinnedTopics), pinnedTopics, unpinnedTopics}, nil
}

// Pin a topic
func (rg *RoostGroup) PinTopic(topicID []byte, pinned bool) error {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("topics", topicID, map[string]interface{}{
		"pinned": pinned,
	})
	return writer.Execute()
}

// Moves a topic item in the given topic specified by id
func (rg *RoostGroup) MoveTopic(pinned bool, from, to int) error {
	var targetTopics []*Topic
	var pos func(*Topic) float64
	var posProp string
	topics, err := rg.Topics()
	if err != nil {
		return err
	}

	if pinned {
		targetTopics = topics.pinnedTopics
		pos = func(t *Topic) float64 {
			return t.PinPosition
		}
		posProp = "pin_position"
	} else {
		targetTopics = topics.unpinnedTopics
		pos = func(t *Topic) float64 {
			return t.Position
		}
		posProp = "position"
	}

	atZero := true
	for _, t := range targetTopics {
		if pos(t) != 0 {
			atZero = false
			break
		}
	}

	writer := rg.roost.slick.EAVWriter(rg.group)
	if atZero {
		for i, t := range targetTopics {
			if pinned {
				t.PinPosition = float64(i)
			} else {
				t.Position = float64(i)
			}
			if i == from {
				continue
			}
			writer.Update("topics", t.ID, map[string]interface{}{
				posProp: float64(i),
			})
		}
	}

	topic := targetTopics[from]

	var newPosition float64
	if to == 0 {
		rg.roost.log.Infof("being")
		newPosition = randomPosition(pos(targetTopics[0])-2, pos(targetTopics[0]))
	} else if to == len(targetTopics)-1 {
		rg.roost.log.Infof("end")
		newPosition = randomPosition(pos(targetTopics[len(targetTopics)-1]), pos(targetTopics[len(targetTopics)-1])+2)
	} else if to > from {
		rg.roost.log.Infof("for")
		newPosition = randomPosition(pos(targetTopics[to]), pos(targetTopics[to+1]))
	} else {
		rg.roost.log.Infof("bagefter")
		newPosition = randomPosition(pos(targetTopics[to-1]), pos(targetTopics[to]))
	}
	rg.roost.log.Infof("old: %f new %f", pos(topic), newPosition)

	writer.Update("topics", topic.ID, map[string]interface{}{
		posProp: newPosition,
	})
	return writer.Execute()
}

// Creates a todo item in the given topic specified by id with a textual body.
func (rg *RoostGroup) CreateTodo(topicID []byte, label string) (*Todo, error) {
	maxPosition := float64(0)
	maxPositionRow := struct {
		MaxPosition *float64 `db:"max_position"`
	}{}
	if err := rg.roost.slick.EAVGet(&maxPositionRow, "select max(position) as max_position from todos where group_id = ? AND topic_id = ? AND deleted = 0 AND completed_at = 0", rg.group.ID[:], topicID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	} else if maxPositionRow.MaxPosition != nil {
		maxPosition = randomPosition(*maxPositionRow.MaxPosition, *maxPositionRow.MaxPosition+2)
	}

	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Insert("todos", map[string]interface{}{
		"body":     label,
		"topic_id": topicID,
		"position": maxPosition,
		"read":     true,
	})
	if err := writer.Execute(); err != nil {
		return nil, err
	}

	return rg.Todo(writer.InsertIDs[0][:])
}

// Create a todo updater which can be used for marking todos complete en masse
func (rg *RoostGroup) TodoUpdater() *TodoUpdater {
	return &TodoUpdater{rg, make(map[ids.ID]bool), make(map[ids.ID]bool)}
}

// Moves a todo item in the given topic specified by id
func (rg *RoostGroup) MoveTodo(complete bool, topicID []byte, from, to int) error {
	var targetTodos []*Todo
	var pos func(*Todo) float64
	var posProp string
	todos, err := rg.Todos(topicID)
	if err != nil {
		return err
	}

	if complete {
		targetTodos = todos.completeTodos
		pos = func(t *Todo) float64 {
			return t.CompletedPosition
		}
		posProp = "completed_position"
	} else {
		targetTodos = todos.incompleteTodos
		pos = func(t *Todo) float64 {
			return t.Position
		}
		posProp = "position"
	}

	todo := targetTodos[from]

	var newPosition float64
	if to == 0 {
		newPosition = randomPosition(pos(targetTodos[0])-2, pos(targetTodos[0]))
	} else if to == len(targetTodos)-1 {
		newPosition = randomPosition(pos(targetTodos[len(targetTodos)-1]), pos(targetTodos[len(targetTodos)-1])+2)
	} else if to > from {
		newPosition = randomPosition(pos(targetTodos[to]), pos(targetTodos[to+1]))
	} else {
		newPosition = randomPosition(pos(targetTodos[to-1]), pos(targetTodos[to]))
	}

	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("todos", todo.ID, map[string]interface{}{
		posProp: newPosition,
	})
	return writer.Execute()
}

// Gets a todo for a given id.
func (rg *RoostGroup) Todo(id []byte) (*Todo, error) {
	todo := Todo{}
	return &todo, rg.roost.slick.EAVGet(&todo, "select * from todos where group_id = ? AND id = ?", rg.group.ID[:], id[:])
}

// Gets a list of todos for a given topic id.
func (rg *RoostGroup) Todos(topicID []byte) (*Todos, error) {
	var incompleteTodos []*Todo
	if err := rg.roost.slick.EAVSelect(&incompleteTodos, "select * from todos where group_id = ? AND topic_id = ? AND deleted = 0 AND completed_at = 0 order by position, id", rg.group.ID[:], topicID[:]); err != nil {
		return nil, err
	}
	var completeTodos []*Todo
	if err := rg.roost.slick.EAVSelect(&completeTodos, "select * from todos where group_id = ? AND topic_id = ? AND deleted = 0 AND completed_at != 0 order by completed_position, id", rg.group.ID[:], topicID[:]); err != nil {
		return nil, err
	}

	return &Todos{len(incompleteTodos), len(completeTodos), incompleteTodos, completeTodos}, nil
}

// Updates a todo.
func (rg *RoostGroup) UpdateTodo(todo *Todo) error {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("todos", todo.ID, map[string]interface{}{
		"body":         todo.Body,
		"topic_id":     todo.TopicID,
		"position":     todo.Position,
		"completed_at": todo.CompletedAt,
		"deleted":      todo.Deleted,
		"read":         todo.Read,
	})
	return writer.Execute()
}

// Set a reaction to an entity
func (rg *RoostGroup) SetReaction(entityID []byte, r string, active bool) error {
	reaction := Reaction{}
	if err := rg.roost.slick.EAVGet(&reaction, "select * from reactions where group_id = ? AND entity_id = ? AND _identity_tag = ? AND rune = ?", rg.group.ID[:], entityID, rg.group.IdentityTag[:], r); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		if c := uniseg.GraphemeClusterCount(r); c != 1 {
			return fmt.Errorf("expected 1 grapheme cluster, got %d", c)
		}

		writer := rg.roost.slick.EAVWriter(rg.group)
		writer.Insert("reactions", map[string]interface{}{
			"active":    active,
			"rune":      r,
			"entity_id": entityID,
		})
		return writer.Execute()
	}

	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("reactions", reaction.ID, map[string]interface{}{
		"active":    active,
		"rune":      string(r),
		"entity_id": entityID,
	})
	return writer.Execute()
}

// Get all reactions for a given entity id
func (rg *RoostGroup) Reactions(entityID []byte) (*Reactions, error) {
	var reactions []*Reaction
	if err := rg.roost.slick.EAVSelect(&reactions, "select * from reactions where group_id = ? AND entity_id = ? AND active = 1 ORDER BY _mtime", rg.group.ID[:], entityID); err != nil {
		return nil, err
	}
	return &Reactions{len(reactions), reactions}, nil
}

// Creates a message in a given topic id with a textual body.
func (rg *RoostGroup) CreateMessage(topicID []byte, body string) (*Message, error) {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Insert("messages", map[string]interface{}{
		"body":     body,
		"topic_id": topicID,
	})
	writer.Update("topics", topicID, map[string]interface{}{
		"message_last_read": now(),
	})
	if err := writer.Execute(); err != nil {
		return nil, err
	}

	return rg.Message(writer.InsertIDs[0][:])
}

// Gets a message for a given id.
func (rg *RoostGroup) Message(id []byte) (*Message, error) {
	message := Message{}
	return &message, rg.roost.slick.EAVGet(&message, "select * from messages where group_id = ? AND id = ?", rg.group.ID[:], id[:])
}

// Gets a list of messages for a given topic id and a cursor. If cursor is "", it retrieves messages in reverse chronological order.
// Otherwise, use the cursor value provided by PagedMessages.
func (rg *RoostGroup) Messages(topicID []byte, cursor string) (*PagedMessages, error) {
	pagedMessages := PagedMessages{}
	if cursor == "" {
		if err := rg.roost.slick.EAVSelect(&pagedMessages.values, "select * from messages where group_id = ? AND topic_id = ? order by _ctime desc, id limit ?", rg.group.ID[:], topicID[:], MessagesPageSize); err != nil {
			return nil, err
		}
	} else {
		cursorFloat, err := strconv.ParseFloat(cursor, 64)
		if err != nil {
			return nil, err
		}
		if err := rg.roost.slick.EAVSelect(&pagedMessages.values, "select * from messages where group_id = ? AND topic_id = ? AND _ctime < ? order by _ctime desc, id limit ?", rg.group.ID[:], topicID[:], cursorFloat, MessagesPageSize); err != nil {
			return nil, err
		}
	}
	pagedMessages.Count = len(pagedMessages.values)
	pagedMessages.AtEnd = len(pagedMessages.values) != MessagesPageSize
	if len(pagedMessages.values) != 0 {
		pagedMessages.Cursor = strconv.FormatFloat(pagedMessages.values[len(pagedMessages.values)-1].CtimeSec, 'f', -1, 64)
	}
	return &pagedMessages, nil
}

// Updates a message.
func (rg *RoostGroup) UpdateMessage(message *Message) error {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("messages", message.ID, map[string]interface{}{
		"body":     message.Body,
		"topic_id": message.TopicID,
	})
	return writer.Execute()
}

// Gets a list of all connected devices to the current identity.
func (rg *RoostGroup) DeleteTodo(id []byte) error {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("todos", id, map[string]interface{}{
		"deleted": true,
	})
	return writer.Execute()
}

// Set show completed
func (rg *RoostGroup) SetShowCompleted(id []byte, completed bool) error {
	writer := rg.roost.slick.EAVWriter(rg.group)
	writer.Update("topics", id, map[string]interface{}{
		"show_completed": completed,
	})
	return writer.Execute()
}
