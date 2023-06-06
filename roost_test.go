package roost

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var password = "zxcvbnasdfghqwertyuzxcvbnasdfghq"

func deleteAll(glob string) {
	if err := os.RemoveAll(glob); err != nil {
		panic(err)
	}
}

func makeRoost(root string) (*Roost, []interface{}, error) {
	deleteAll(root)
	events := make([]interface{}, 0)
	roost, err := MakeRoostWithStrongKey(root, "")
	if err != nil {
		return nil, events, err
	}

	go func() {
		updates := roost.Updates()
		for {
			updates.Next()
			events = append(events, updates.item)
			if updates.Type() == UpdateFinished {
				break
			}
		}
	}()
	return roost, events, err
}

func getTodoBodies(complete bool, todos *Todos) []string {
	var targetTodos []*Todo
	if complete {
		targetTodos = todos.completeTodos
	} else {
		targetTodos = todos.incompleteTodos
	}
	bodies := make([]string, len(targetTodos))
	for i := 0; i != len(targetTodos); i++ {
		bodies[i] = targetTodos[i].Body
	}
	return bodies
}

func getTopicLabels(pinned bool, topics *Topics) []string {
	var targetTopics []*Topic
	if pinned {
		targetTopics = topics.pinnedTopics
	} else {
		targetTopics = topics.unpinnedTopics
	}
	bodies := make([]string, len(targetTopics))
	for i := 0; i != len(targetTopics); i++ {
		bodies[i] = targetTopics[i].Label
	}
	return bodies
}

func teardownRoost(r *Roost, p string) {
	err := r.Shutdown()
	deleteAll(p)
	if err != nil {
		panic(err)
	}
}

func TestRoostInit(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal(1, topics.Count)
}

func TestRoostShutdown(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	u := roost1.Updates()
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	teardownRoost(roost1, "roost1")
	for {
		u.Next()
		if u.Type() == UpdateFinished {
			break
		}
	}
}

func TestRoostCreateTodo(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal(1, topics.Count)
	todo1, err := group.CreateTodo(topics.Topic(0).ID, "need to mow the lawn")
	require.Nil(err)
	require.Equal(float64(0), todo1.CompletedAt)
	require.Equal(false, todo1.Deleted)
	todo2, err := group.CreateTodo(topics.Topic(0).ID, "need to mow the lawn more")
	require.Nil(err)
	require.Equal(float64(0), todo1.CompletedAt)
	require.Equal(false, todo2.Deleted)
	require.Greater(todo2.Position, todo1.Position)

	todos, err := group.Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(2, todos.IncompleteCount)
	require.Equal(0, todos.CompleteCount)
	topic, err := group.Topic(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(2, topic.IncompleteTodoCount)
}

func TestRoostTodoUpdater(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal(1, topics.Count)
	todo1, err := group.CreateTodo(topics.Topic(0).ID, "need to mow the lawn")
	require.Nil(err)
	require.Equal(float64(0), todo1.CompletedAt)
	require.Equal(false, todo1.Deleted)
	_, err = group.CreateTodo(topics.Topic(0).ID, "need to mow the lawn more")
	require.Nil(err)

	todos, err := group.Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(2, todos.IncompleteCount)
	require.Equal(0, todos.CompleteCount)

	tu := group.TodoUpdater()
	tu.MarkComplete(todo1.ID, true)
	require.Nil(tu.Commit())
	todos, err = group.Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(1, todos.IncompleteCount)
	require.Equal(1, todos.CompleteCount)
}

func TestRoostUpdateTodo(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	todo, err := group.CreateTodo(topics.Topic(0).ID, "need to mow the lawn")
	require.Nil(err)
	topic, err := group.Topic(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(1, topic.IncompleteTodoCount)
	todo.Body = "still need to mow the lawn"
	todo.CompletedAt = now()
	todo.Read = true
	todo.Deleted = true
	require.Nil(group.UpdateTodo(todo))
	getTodo, err := group.Todo(todo.ID)
	require.Nil(err)
	require.Equal("still need to mow the lawn", getTodo.Body)
	require.NotEqual(float64(0), getTodo.CompletedAt)
	require.Equal(true, getTodo.Read)
	require.Equal(true, getTodo.Deleted)
	topic, err = group.Topic(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(0, topic.IncompleteTodoCount)
}

func TestRoostDeleteTodo(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	todo, err := group.CreateTodo(topics.Topic(0).ID, "need to mow the lawn")
	require.Nil(err)
	topic, err := group.Topic(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(1, topic.IncompleteTodoCount)
	require.Nil(group.DeleteTodo(todo.ID))
	topics, err = group.Topics()
	require.Nil(err)
	require.Equal(0, topics.Topic(0).IncompleteTodoCount)
}

func TestRoostMoveTodoInList(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "1")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "2")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "3")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "4")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "5")
	require.Nil(err)
	require.Nil(group.MoveTodo(false, topics.Topic(0).ID, 3, 2))
	todos, err := group.Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal([]string{"1", "2", "4", "3", "5"}, getTodoBodies(false, todos))
}

func TestRoostMoveTodoBeginningOfList(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "1")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "2")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "3")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "4")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "5")
	require.Nil(err)
	require.Nil(group.MoveTodo(false, topics.Topic(0).ID, 3, 0))
	todos, err := group.Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal([]string{"4", "1", "2", "3", "5"}, getTodoBodies(false, todos))
}

func TestRoostMoveTodoEndOfList(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "1")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "2")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "3")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "4")
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "5")
	require.Nil(err)
	require.Nil(group.MoveTodo(false, topics.Topic(0).ID, 3, 4))
	todos, err := group.Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal([]string{"1", "2", "3", "5", "4"}, getTodoBodies(false, todos))
}

func TestRoostCreateTopic(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal(1, topics.Count)
	topic, err := group.CreateTopic("some topic")
	require.Nil(err)
	require.Equal("some topic", topic.Label)
}

func TestRoostMoveTopicInList(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	_, err = group.CreateTopicPinned("one", true)
	require.Nil(err)
	_, err = group.CreateTopicPinned("two", true)
	require.Nil(err)
	_, err = group.CreateTopicPinned("three", true)
	require.Nil(err)
	_, err = group.CreateTopicPinned("four", false)
	require.Nil(err)
	_, err = group.CreateTopicPinned("five", false)
	require.Nil(err)
	_, err = group.CreateTopicPinned("six", false)
	require.Nil(err)
	_, err = group.CreateTopicPinned("seven", false)
	require.Nil(err)

	require.Nil(group.MoveTopic(false, 1, 2))
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal([]string{"home", "five", "four", "six", "seven"}, getTopicLabels(false, topics))
}

func TestRoostMoveTopicInListFromZeroList(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	_, err = group.createTopicPinned("one", true, 0)
	require.Nil(err)
	_, err = group.createTopicPinned("two", true, 0)
	require.Nil(err)
	_, err = group.createTopicPinned("three", true, 0)
	require.Nil(err)
	_, err = group.createTopicPinned("four", false, 0)
	require.Nil(err)
	_, err = group.createTopicPinned("five", false, 0)
	require.Nil(err)
	_, err = group.createTopicPinned("six", false, 0)
	require.Nil(err)
	_, err = group.createTopicPinned("seven", false, 0)
	require.Nil(err)

	require.Nil(group.MoveTopic(false, 1, 2))
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal([]string{"home", "five", "four", "six", "seven"}, getTopicLabels(false, topics))
}

func TestRoostUpdateTopic(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal(1, topics.Count)
	topic, err := group.CreateTopic("some topic")
	require.Nil(err)
	require.Equal("some topic", topic.Label)
	topic.Label = "new topic"
	require.Nil(group.UpdateTopic(topic))
	getTopic, err := group.Topic(topic.ID)
	require.Nil(err)
	require.Equal("new topic", getTopic.Label)
}

func TestRoostCreateMessages(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	for i := 0; i != 43; i++ {
		_, err := group.CreateMessage(topics.Topic(0).ID, fmt.Sprintf("hello %d", i))
		require.Nil(err)
	}
	page1, err := group.Messages(topics.Topic(0).ID, "")
	require.Nil(err)
	require.Equal(20, page1.Count)
	require.Equal(false, page1.AtEnd)
	page2, err := group.Messages(topics.Topic(0).ID, page1.Cursor)
	require.Nil(err)
	require.Equal(20, page2.Count)
	require.Equal(false, page2.AtEnd)
	page3, err := group.Messages(topics.Topic(0).ID, page2.Cursor)
	require.Nil(err)
	require.Equal(3, page3.Count)
	require.Equal(true, page3.AtEnd)
}

func TestRoostMarkMessageRead(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group1, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics1, err := group1.Topics()
	require.Nil(err)
	_, err = group1.CreateMessage(topics1.Topic(0).ID, "hello there")
	require.Nil(err)

	roost2, _, err := makeRoost("roost2")
	defer teardownRoost(roost2, "roost2")
	require.Nil(err)
	require.Nil(roost2.Initialize(password))

	invite, err := group1.Invite("invite password")
	require.Nil(err)

	_, err = roost2.AcceptInvite(invite, "invite password")
	require.Nil(err)
	require.Eventually(func() bool {
		groups, err := roost2.Groups()
		require.Nil(err)
		return groups.Count == 1
	}, 10*time.Second, 100*time.Millisecond)

	groups2, err := roost2.Groups()
	require.Nil(err)
	topics2, err := groups2.Group(0).Topics()
	require.Nil(err)
	topic2 := topics2.Topic(0)
	require.Nil(err)
	require.Equal(1, topic2.UnreadMessageCount)
	require.Nil(groups2.Group(0).MarkTopicRead(topic2.ID))
	topic2, err = groups2.Group(0).Topic(topic2.ID)
	require.Nil(err)
	require.Equal(0, topic2.UnreadMessageCount)
}

func TestRoostUpdateMessage(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	newTopic, err := group.CreateTopic("some topic")
	require.Nil(err)
	message, err := group.CreateMessage(topics.Topic(0).ID, "hello there")
	require.Equal("hello there", message.Body)
	require.Equal(topics.Topic(0).ID, message.TopicID)
	require.Nil(err)
	message.Body = "hello there again"
	message.TopicID = newTopic.ID
	require.Nil(group.UpdateMessage(message))
	getMessage, err := group.Message(message.ID)
	require.Nil(err)
	require.Equal("hello there again", getMessage.Body)
	require.Equal(newTopic.ID, getMessage.TopicID)
}

func TestRoostReactionsSetAndUnset(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	message, err := group.CreateMessage(topics.Topic(0).ID, "hello there")
	require.Equal("hello there", message.Body)
	require.Equal(topics.Topic(0).ID, message.TopicID)
	require.Nil(err)
	require.Nil(group.SetReaction(message.ID, "😶‍🌫️", true))
	require.Nil(group.SetReaction(message.ID, "😅", true))
	reactions, err := group.Reactions(message.ID)
	require.Nil(err)
	require.Equal(2, reactions.Count)
	require.Nil(group.SetReaction(message.ID, "😶‍🌫️", false))
	reactions, err = group.Reactions(message.ID)
	require.Nil(err)
	require.Equal(1, reactions.Count)
}

func TestRoostReactionsTooManyRunes(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	message, err := group.CreateMessage(topics.Topic(0).ID, "hello there")
	require.Equal("hello there", message.Body)
	require.Equal(topics.Topic(0).ID, message.TopicID)
	require.Nil(err)
	err = group.SetReaction(message.ID, "😶‍🌫️😀", true)
	require.ErrorContains(err, "got 2")
}

func TestExternalInvite(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "hey there!")
	require.Nil(err)

	roost2, _, err := makeRoost("roost2")
	defer teardownRoost(roost2, "roost2")
	require.Nil(err)
	require.Nil(roost2.Initialize(password))

	invite, err := group.Invite("invite password")
	require.Nil(err)

	_, err = roost2.AcceptInvite(invite, "invite password")
	require.Nil(err)
	require.Eventually(func() bool {
		groups, err := roost2.Groups()
		require.Nil(err)
		return groups.Count == 1
	}, 10*time.Second, 100*time.Millisecond)

	groups, err := roost2.Groups()
	require.Nil(err)
	todos, err := groups.Group(0).Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(1, todos.IncompleteCount)
	require.Equal("hey there!", todos.IncompleteTodo(0).Body)
	require.Equal(0, todos.CompleteCount)
}

func TestDeviceGroupInvite(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	_, err = group.CreateTodo(topics.Topic(0).ID, "hey there!")
	require.Nil(err)

	roost2, _, err := makeRoost("roost2")
	defer teardownRoost(roost2, "roost2")
	require.Nil(err)
	require.Nil(roost2.Initialize(password))

	require.Nil(roost1.SetDeviceNameType("this is roost1", "Desktop"))
	require.Nil(roost2.SetDeviceNameType("this is roost2", "iPhone"))

	link, err := roost1.GetDeviceLink()
	require.Nil(err)

	require.Nil(roost2.LinkDevice(link))

	require.Eventually(func() bool {
		devices, err := roost1.Devices()
		require.Nil(err)
		return devices.Count == 2
	}, 2*time.Second, 50*time.Millisecond)

	devices, err := roost1.Devices()
	require.Nil(err)

	require.Equal("this is roost1", devices.Device(0).Name())
	require.Equal("Desktop", devices.Device(0).Type())
	require.Equal("this is roost2", devices.Device(1).Name())
	require.Equal("iPhone", devices.Device(1).Type())

	require.Eventually(func() bool {
		devices, err := roost2.Devices()
		require.Nil(err)
		return devices.Count == 2
	}, 2*time.Second, 50*time.Millisecond)

	devices, err = roost2.Devices()
	require.Nil(err)

	require.Equal("this is roost1", devices.Device(0).Name())
	require.Equal("Desktop", devices.Device(0).Type())
	require.Equal("this is roost2", devices.Device(1).Name())
	require.Equal("iPhone", devices.Device(1).Type())

	require.Eventually(func() bool {
		groups, err := roost2.Groups()
		require.Nil(err)
		return groups.Count == 1
	}, 5*time.Second, 100*time.Millisecond)

	groups, err := roost2.Groups()
	require.Nil(err)
	todos, err := groups.Group(0).Todos(topics.Topic(0).ID)
	require.Nil(err)
	require.Equal(1, todos.IncompleteCount)
	require.Equal("hey there!", todos.IncompleteTodo(0).Body)
}

func TestRoostSearchGroup(t *testing.T) {
	require := require.New(t)
	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)
	require.Nil(roost1.Initialize(password))
	group, err := roost1.CreateGroup("grou1")
	require.Nil(err)
	topics, err := group.Topics()
	require.Nil(err)
	require.Equal(1, topics.Count)
	todo1, err := group.CreateTodo(topics.Topic(0).ID, "mow the lawn")
	require.Nil(err)
	require.Equal(float64(0), todo1.CompletedAt)
	require.Equal(false, todo1.Deleted)
	todo2, err := group.CreateTodo(topics.Topic(0).ID, "make my bed")
	require.Nil(err)
	require.Equal(float64(0), todo2.CompletedAt)
	require.Equal(false, todo2.Deleted)
	require.Greater(todo2.Position, todo1.Position)

	result, err := roost1.Search(group.GroupID, "lawn", "<b>", "</b>")
	require.Nil(err)
	require.Equal("mow the <b>lawn</b>", result.Result(0).Text)
	require.Equal(todo1.ID, result.Result(0).EntityID)
	require.Equal("todo", result.Result(0).Type)
}

func TestRoostUnreadMessageCounts(t *testing.T) {
	require := require.New(t)

	roost1, _, err := makeRoost("roost1")
	defer teardownRoost(roost1, "roost1")
	require.Nil(err)

	require.Nil(roost1.Initialize(password))
	group1, err := roost1.CreateGroup("group1")
	require.Nil(err)
	topic1_1, err := group1.CreateTopic("hi")
	require.Nil(err)
	topic1_2, err := group1.CreateTopic("hi2")
	require.Nil(err)

	group2, err := roost1.CreateGroup("group2")
	require.Nil(err)
	topic2_1, err := group2.CreateTopic("hi")
	require.Nil(err)
	topic2_2, err := group2.CreateTopic("hi2")
	require.Nil(err)

	group3, err := roost1.CreateGroup("group3")
	require.Nil(err)
	topic3_1, err := group3.CreateTopic("hi")
	require.Nil(err)
	topic3_2, err := group3.CreateTopic("hi2")
	require.Nil(err)

	writeMessage := func(group *RoostGroup, topicID []byte) {
		group.group.AuthorTag = [7]byte{1, 2, 3, 4, 5, 6, 7}
		writer := roost1.slick.EAVWriter(group.group)
		writer.Insert("messages", map[string]interface{}{
			"body":     "hi",
			"topic_id": topicID,
		})
		require.Nil(writer.Execute())
		require.Nil(err)

	}
	writeMessage(group1, topic1_1.ID)
	writeMessage(group1, topic1_1.ID)
	writeMessage(group1, topic1_2.ID)
	writeMessage(group2, topic2_1.ID)
	writeMessage(group2, topic2_2.ID)
	writeMessage(group3, topic3_1.ID)
	writeMessage(group3, topic3_2.ID)
	writeMessage(group3, topic3_2.ID)

	count, err := roost1.UnreadMessageCount()
	require.Nil(err)
	require.Equal(int64(8), count)
}
