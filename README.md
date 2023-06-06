# Roost

This is a library for providing core functionallity neccesary for the Roost app. Roost is a life organizing app which is offline-first and is private by design.

## Concepts

Roost is powered by Slick which in turn uses SQLCipher.

### Identity

An identity in Roost comprises the constellation of devices you have Roost installed on and use.

### Device

Either a phone, iPad or computer capable of running Roost.

### Group

A set of identites you share a common database with. Colloquolly referred to as a "roost".

### Topic

An organizing principle within a group to separate things.

### Message

A message sent in a group that belongs to a specific topic.

### Todo

A todo item in a group that belongs to a specific topic.

## Application lifecycle

The Roost application is in the `new` state when it is initially created. As the sqlite database used is password protected,
to initialize roost you need to provide a password. This is done by calling `Initialize(password)` with the desired password. This
causes Roost to transition to the `running` state. After this has been done, subsequent creation of the Roost application will
start in the `locked` state. This is then transitioned to the `running` state by calling `Unlock(password)`. Users of the library
can rely on platform-specific features to store this password or otherwise prompt the user to supply their own password.

To cleanly shutdown Roost call `Shutdown`. After this is called, the Roost instance should no longer be used.

```
new ----> running
            ^
locked -----/

```

### Joining a device group

An initialized Roost application can join the device group for another identity by providing it's URLs to the existing
identity, which in turns invites the device. Once that invite is approved, the device belongs to the device group
and all data will be synced between both devices.

The diagram below illustrates newly created `roost1` joining an identity which `roost2` belongs to.

```
roost1                            roost2

GetDeviceLink()
           --- send via QR code ---->
                                  LinkDevice(link)
```

### Leaving a device group

To leave a device group, call `LeaveDeviceGroup()`. This will destory all data on this device and return this Roost to a `new` state.

### Device identification

Devices are identified by a name and type. These are arbitrary strings and its up to various implementing platforms to display these
in a way that is useful to the user. Device names and types are not shared outside of your device group.

### Updates

Notifications about updates to application state, updates to the data and errors encountered are provided by a channel which
is returned by `Updates()`.

### Push notifications

Devices can register for push notifications by calling `AddPushToken(token)` and removing that token by calling `DeletePushToken(token)`.

## Groups

Groups are a set of identities you share a database with. Within a group you have any number of topics, which in turn contain any number of
todos and messages.

### Joining a group

To join a group with a person with whom you have joined no prior groups, you use the following steps.

```
joinee                            joiner
(has group)                       (wants to join group)

Invite(password)
               --> send QR code
                   via email ---> AcceptInvite(invite, password)
```
