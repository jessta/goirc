package irc

// Here you'll find the Channel and Nick structs
// as well as the internal state maintenance code for the handlers

import (
	"fmt"
	"reflect"
)

// A struct representing an IRC channel
type Channel struct {
	Name, Topic string
	Modes       *ChanMode
	Nicks       map[*Nick]*ChanPrivs
	conn        *Conn
}

// A struct representing an IRC nick
type Nick struct {
	Nick, Ident, Host, Name string
	Modes                   *NickMode
	Channels                map[*Channel]*ChanPrivs
	conn                    *Conn
}

// A struct representing the modes of an IRC Channel
// (the ones we care about, at least).
//
// See the MODE handler in setupEvents() for details of how this is maintained.
type ChanMode struct {
	// MODE +p, +s, +t, +n, +m
	Private, Secret, ProtectedTopic, NoExternalMsg, Moderated bool

	// MODE +i, +O, +z
	InviteOnly, OperOnly, SSLOnly bool

	// MODE +k
	Key string

	// MODE +l
	Limit int
}

// A struct representing the modes of an IRC Nick (User Modes)
// (again, only the ones we care about)
//
// This is only really useful for conn.Me, as we can't see other people's modes
// without IRC operator privileges (and even then only on some IRCd's).
type NickMode struct {
	// MODE +i, +o, +w, +x, +z
	Invisible, Oper, WallOps, HiddenHost, SSL bool
}

// A struct representing the modes a Nick can have on a Channel
type ChanPrivs struct {
	// MODE +q, +a, +o, +h, +v
	Owner, Admin, Op, HalfOp, Voice bool
}

/******************************************************************************\
 * Conn methods to create/look up nicks/channels
\******************************************************************************/

// Creates a new *irc.Nick, initialises it, and stores it in *irc.Conn so it
// can be properly tracked for state management purposes.
func (conn *Conn) NewNick(nick, ident, name, host string) *Nick {
	n := &Nick{Nick: nick, Ident: ident, Name: name, Host: host, conn: conn}
	n.initialise()
	conn.nicks[n.Nick] = n
	return n
}

// Returns an *irc.Nick for the nick n, if we're tracking it.
func (conn *Conn) GetNick(n string) *Nick {
	if nick, ok := conn.nicks[n]; ok {
		return nick
	}
	return nil
}

// Creates a new *irc.Channel, initialises it, and stores it in *irc.Conn so it
// can be properly tracked for state management purposes.
func (conn *Conn) NewChannel(c string) *Channel {
	ch := &Channel{Name: c, conn: conn}
	ch.initialise()
	conn.chans[ch.Name] = ch
	return ch
}

// Returns an *irc.Channel for the channel c, if we're tracking it.
func (conn *Conn) GetChannel(c string) *Channel {
	if ch, ok := conn.chans[c]; ok {
		return ch
	}
	return nil
}

/******************************************************************************\
 * Channel methods for state management
\******************************************************************************/

func (ch *Channel) initialise() {
	ch.Modes = new(ChanMode)
	ch.Nicks = make(map[*Nick]*ChanPrivs)
}

// Associates an *irc.Nick with an *irc.Channel using a shared *irc.ChanPrivs
func (ch *Channel) AddNick(n *Nick) {
	if _, ok := ch.Nicks[n]; !ok {
		ch.Nicks[n] = new(ChanPrivs)
		n.Channels[ch] = ch.Nicks[n]
	} else {
		ch.conn.error("irc.Channel.AddNick() warning: trying to add already-present nick %s to channel %s", n.Nick, ch.Name)
	}
}

// Disassociates an *irc.Nick from an *irc.Channel. Will call ch.Delete() if
// the *irc.Nick being removed is the connection's nick. Will also call
// n.DelChannel(ch) to remove the association from the perspective of *irc.Nick.
func (ch *Channel) DelNick(n *Nick) {
	if _, ok := ch.Nicks[n]; ok {
		if n == n.conn.Me {
			// we're leaving the channel, so remove all state we have about it
			ch.Delete()
		} else {
			ch.Nicks[n] = nil, false
			n.DelChannel(ch)
		}
	} // no else here ...
	// we call Channel.DelNick() and Nick.DelChan() from each other to ensure
	// consistency, and this would mean spewing an error message every delete
}

// Stops the channel from being tracked by state tracking handlers. Also calls
// n.DelChannel(ch) for all nicks that are associated with the channel.
func (ch *Channel) Delete() {
	for n, _ := range ch.Nicks {
		n.DelChannel(ch)
	}
	ch.conn.chans[ch.Name] = nil, false
}

/******************************************************************************\
 * Nick methods for state management
\******************************************************************************/
func (n *Nick) initialise() {
	n.Modes = new(NickMode)
	n.Channels = make(map[*Channel]*ChanPrivs)
}

// Associates an *irc.Channel with an *irc.Nick using a shared *irc.ChanPrivs
//
// Very slightly different to irc.Channel.AddNick() in that it tests for a
// pre-existing association within the *irc.Nick object rather than the
// *irc.Channel object before associating the two. 
func (n *Nick) AddChannel(ch *Channel) {
	if _, ok := n.Channels[ch]; !ok {
		ch.Nicks[n] = new(ChanPrivs)
		n.Channels[ch] = ch.Nicks[n]
	} else {
		n.conn.error("irc.Nick.AddChannel() warning: trying to add already-present channel %s to nick %s", ch.Name, n.Nick)
	}
}

// Disassociates an *irc.Channel from an *irc.Nick. Will call n.Delete() if
// the *irc.Nick is no longer on any channels we are tracking. Will also call
// ch.DelNick(n) to remove the association from the perspective of *irc.Channel.
func (n *Nick) DelChannel(ch *Channel) {
	if _, ok := n.Channels[ch]; ok {
		n.Channels[ch] = nil, false
		ch.DelNick(n)
		if len(n.Channels) == 0 {
			// nick is no longer in any channels we inhabit, stop tracking it
			n.Delete()
		}
	}
}

// Signals to the tracking code that the *irc.Nick object should be tracked
// under a "neu" nick rather than the old one.
func (n *Nick) ReNick(neu string) {
	n.conn.nicks[n.Nick] = nil, false
	n.Nick = neu
	n.conn.nicks[n.Nick] = n
}

// Stops the nick from being tracked by state tracking handlers. Also calls
// ch.DelNick(n) for all nicks that are associated with the channel.
func (n *Nick) Delete() {
	// we don't ever want to remove *our* nick from conn.nicks...
	if n != n.conn.Me {
		for ch, _ := range n.Channels {
			ch.DelNick(n)
		}
		n.conn.nicks[n.Nick] = nil, false
	}
}

/******************************************************************************\
 * String() methods for all structs in this file for ease of debugging.
\******************************************************************************/

// Map *irc.ChanMode fields to IRC mode characters
var ChanModeToString = map[string]string{
	"Private": "p",
	"Secret": "s",
	"ProtectedTopic": "t",
	"NoExternalMsg": "n",
	"Moderated": "m",
	"InviteOnly": "i",
	"OperOnly": "O",
	"SSLOnly": "z",
	"Key": "k",
	"Limit": "l",
}

// Map *irc.NickMode fields to IRC mode characters
var NickModeToString = map[string]string{
	"Invisible": "i",
	"Oper": "o",
	"WallOps": "w",
	"HiddenHost": "x",
	"SSL": "z",
}

// Map *irc.ChanPrivs fields to IRC mode characters
var ChanPrivToString = map[string]string{
	"Owner": "q",
	"Admin": "a",
	"Op": "o",
	"HalfOp": "h",
	"Voice": "v",
}

// Map *irc.ChanPrivs fields to the symbols used to represent these modes
// in NAMES and WHOIS responses
var ChanPrivToModeChar = map[string]byte{
	"Owner": '~',
	"Admin": '&',
	"Op": '@',
	"HalfOp": '%',
	"Voice": '+',
}

// Reverse mappings of the above datastructures
var StringToChanMode, StringToNickMode, StringToChanPriv map[string]string
var ModeCharToChanPriv map[byte]string

// Init function to fill in reverse mappings for *toString constants etc.
func init() {
	StringToChanMode = make(map[string]string)
	for k, v := range ChanModeToString {
		StringToChanMode[v] = k
	}
	StringToNickMode = make(map[string]string)
	for k, v := range NickModeToString {
		StringToNickMode[v] = k
	}
	StringToChanPriv = make(map[string]string)
	for k, v := range ChanPrivToString {
		StringToChanPriv[v] = k
	}
	ModeCharToChanPriv = make(map[byte]string)
	for k, v := range ChanPrivToModeChar {
		ModeCharToChanPriv[v] = k
	}
}

// Returns a string representing the channel. Looks like:
//	Channel: <channel name> e.g. #moo
//	Topic: <channel topic> e.g. Discussing the merits of cows!
//	Mode: <channel modes> e.g. +nsti
//	Nicks:
//		<nick>: <privs> e.g. CowMaster: +o
//		...
func (ch *Channel) String() string {
	str := "Channel: " + ch.Name + "\n\t"
	str += "Topic: " + ch.Topic + "\n\t"
	str += "Modes: " + ch.Modes.String() + "\n\t"
	str += "Nicks: \n"
	for n, p := range ch.Nicks {
		str += "\t\t" + n.Nick + ": " + p.String() + "\n"
	}
	return str
}

// Returns a string representing the nick. Looks like:
//	Nick: <nick name> e.g. CowMaster
//	Hostmask: <ident@host> e.g. moo@cows.org
//	Real Name: <real name> e.g. Steve "CowMaster" Bush
//	Modes: <nick modes> e.g. +z
//	Channels:
//		<channel>: <privs> e.g. #moo: +o
//		...
func (n *Nick) String() string {
	str := "Nick: " + n.Nick + "\n\t"
	str += "Hostmask: " + n.Ident + "@" + n.Host + "\n\t"
	str += "Real Name: " + n.Name + "\n\t"
	str += "Modes: " + n.Modes.String() + "\n\t"
	str += "Channels: \n"
	for ch, p := range n.Channels {
		str += "\t\t" + ch.Name + ": " + p.String() + "\n"
	}
	return str
}

// Returns a string representing the channel modes. Looks like:
//	+npk key
func (cm *ChanMode) String() string {
	str := "+"
	a := make([]string, 2)
	v := reflect.Indirect(reflect.NewValue(cm)).(*reflect.StructValue)
	t := v.Type().(*reflect.StructType)
	for i := 0; i < v.NumField(); i++ {
		switch f := v.Field(i).(type) {
		case *reflect.BoolValue:
			if f.Get() {
				str += ChanModeToString[t.Field(i).Name]
			}
		case *reflect.StringValue:
			if f.Get() != "" {
				str += ChanModeToString[t.Field(i).Name]
				a[0] = f.Get()
			}
		case *reflect.IntValue:
			if f.Get() != 0 {
				str += ChanModeToString[t.Field(i).Name]
				a[1] = fmt.Sprintf("%d", cm.Limit)
			}
		}
	}
	for _, s := range a {
		if s != "" {
			str += " " + s
		}
	}
	if str == "+" {
		str = "No modes set"
	}
	return str
}

// Returns a string representing the nick modes. Looks like:
//	+iwx
func (nm *NickMode) String() string {
	str := "+"
	v := reflect.Indirect(reflect.NewValue(nm)).(*reflect.StructValue)
	t := v.Type().(*reflect.StructType)
	for i := 0; i < v.NumField(); i++ {
		switch f := v.Field(i).(type) {
		// only bools here at the mo!
		case *reflect.BoolValue:
			if f.Get() {
				str += NickModeToString[t.Field(i).Name]
			}
		}
	}
	if str == "+" {
		str = "No modes set"
	}
	return str
}

// Returns a string representing the channel privileges. Looks like:
//	+o
func (p *ChanPrivs) String() string {
	str := "+"
	v := reflect.Indirect(reflect.NewValue(p)).(*reflect.StructValue)
	t := v.Type().(*reflect.StructType)
	for i := 0; i < v.NumField(); i++ {
		switch f := v.Field(i).(type) {
		// only bools here at the mo too!
		case *reflect.BoolValue:
			if f.Get() {
				str += ChanPrivToString[t.Field(i).Name]
			}
		}
	}
	if str == "+" {
		str = "No modes set"
	}
	return str
}
