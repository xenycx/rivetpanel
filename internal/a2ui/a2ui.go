// Package a2ui checks the A2UI v1.0 messages (https://a2ui.org) the
// assistant sends to the chat window, against the RivetPanel catalog.
//
// The assistant describes a small interface (cards, choices, forms, metrics)
// as a flat list of components plus a data model; the browser draws it with
// the panel's own controls. Nothing here executes code: components come from
// a fixed catalog, functions from a fixed list, and links and images are
// limited to safe addresses. The browser repeats the address checks.
package a2ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	// Version is the A2UI protocol version the panel speaks.
	Version = "1.0"
	// BasicCatalogID is the A2UI v1.0 basic catalog. The panel draws its
	// components except Video and AudioPlayer.
	BasicCatalogID = "https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json"
	// CatalogID is the RivetPanel catalog: the basic catalog plus Heading,
	// Metric, Badge, Code and Link. It is the default.
	CatalogID = "urn:rivetpanel:a2ui:catalog:v1"

	MaxMessages   = 20       // messages in one show_ui call
	MaxSurfaces   = 24       // live surfaces in one conversation
	MaxComponents = 120      // components on one surface
	MaxDataBytes  = 32 << 10 // one data model value, encoded
	maxContext    = 8 << 10  // an action's resolved context, encoded
	maxString     = 4000     // any literal string
)

var (
	idRe     = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	actionRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
)

type kind int

const (
	kString kind = iota
	kDynString
	kDynNumber
	kDynBool
	kDynStringList
	kDynValue
	kNumber
	kInteger
	kBool
	kChild
	kChildList
	kEnum
	kAction
	kTabs
	kOptions
	kIcon
	kImageURL
	kHref
	kChecks
	kAccessibility
	kObject
)

type prop struct {
	k    kind
	enum []string
}

type spec struct {
	required  []string
	props     map[string]prop
	checkable bool
	custom    bool // RivetPanel catalog only
}

func enum(v ...string) prop { return prop{k: kEnum, enum: v} }

var justify = enum("start", "center", "end", "spaceBetween", "spaceAround", "spaceEvenly", "stretch")
var align = enum("start", "center", "end", "stretch")

// Icons are the basic catalog's icon names.
var Icons = []string{"accountCircle", "add", "arrowBack", "arrowForward", "attachFile", "calendarToday", "call", "camera", "check", "close", "delete", "download", "edit", "event", "error", "fastForward", "favorite", "favoriteOff", "folder", "help", "home", "info", "locationOn", "lock", "lockOpen", "mail", "menu", "moreVert", "moreHoriz", "notificationsOff", "notifications", "pause", "payment", "person", "phone", "photo", "play", "print", "refresh", "rewind", "search", "send", "settings", "share", "shoppingCart", "skipNext", "skipPrevious", "star", "starHalf", "starOff", "stop", "upload", "visibility", "visibilityOff", "volumeDown", "volumeMute", "volumeOff", "volumeUp", "warning"}

// Tones colour Metric and Badge.
var Tones = []string{"neutral", "good", "warn", "bad", "info"}

var catalog = map[string]spec{
	"Text":          {required: []string{"text"}, props: map[string]prop{"text": {k: kDynString}, "variant": enum("caption", "body")}},
	"Image":         {required: []string{"url"}, props: map[string]prop{"url": {k: kImageURL}, "description": {k: kDynString}, "fit": enum("contain", "cover", "fill", "none", "scaleDown"), "variant": enum("icon", "avatar", "smallFeature", "mediumFeature", "largeFeature", "header")}},
	"Icon":          {required: []string{"name"}, props: map[string]prop{"name": {k: kIcon}}},
	"Row":           {required: []string{"children"}, props: map[string]prop{"children": {k: kChildList}, "justify": justify, "align": align}},
	"Column":        {required: []string{"children"}, props: map[string]prop{"children": {k: kChildList}, "justify": justify, "align": align}},
	"List":          {required: []string{"children"}, props: map[string]prop{"children": {k: kChildList}, "direction": enum("vertical", "horizontal"), "align": align}},
	"Card":          {required: []string{"child"}, props: map[string]prop{"child": {k: kChild}}},
	"Tabs":          {required: []string{"tabs"}, props: map[string]prop{"tabs": {k: kTabs}}},
	"Modal":         {required: []string{"trigger", "content"}, props: map[string]prop{"trigger": {k: kChild}, "content": {k: kChild}}},
	"Divider":       {props: map[string]prop{"axis": enum("horizontal", "vertical")}},
	"Button":        {required: []string{"child", "action"}, checkable: true, props: map[string]prop{"child": {k: kChild}, "action": {k: kAction}, "variant": enum("default", "primary", "borderless")}},
	"TextField":     {required: []string{"label", "value"}, checkable: true, props: map[string]prop{"label": {k: kDynString}, "value": {k: kDynString}, "placeholder": {k: kDynString}, "variant": enum("shortText", "longText", "number")}},
	"CheckBox":      {required: []string{"label", "value"}, checkable: true, props: map[string]prop{"label": {k: kDynString}, "value": {k: kDynBool}}},
	"ChoicePicker":  {required: []string{"options", "value"}, checkable: true, props: map[string]prop{"options": {k: kOptions}, "value": {k: kDynStringList}, "label": {k: kDynString}, "variant": enum("mutuallyExclusive", "multipleSelection"), "displayStyle": enum("checkbox", "chips"), "filterable": {k: kBool}}},
	"Slider":        {required: []string{"value", "max"}, checkable: true, props: map[string]prop{"value": {k: kDynNumber}, "max": {k: kNumber}, "min": {k: kNumber}, "label": {k: kDynString}, "steps": {k: kInteger}}},
	"DateTimeInput": {required: []string{"value"}, checkable: true, props: map[string]prop{"value": {k: kDynString}, "enableDate": {k: kBool}, "enableTime": {k: kBool}, "min": {k: kDynString}, "max": {k: kDynString}, "label": {k: kDynString}}},

	"Heading": {custom: true, required: []string{"text"}, props: map[string]prop{"text": {k: kDynString}}},
	"Metric":  {custom: true, required: []string{"label", "value"}, props: map[string]prop{"label": {k: kDynString}, "value": {k: kDynValue}, "unit": {k: kDynString}, "detail": {k: kDynString}, "tone": {k: kDynString}, "max": {k: kDynNumber}}},
	"Badge":   {custom: true, required: []string{"text"}, props: map[string]prop{"text": {k: kDynString}, "tone": {k: kDynString}}},
	"Code":    {custom: true, required: []string{"text"}, props: map[string]prop{"text": {k: kDynString}, "language": {k: kString}}},
	"Link":    {custom: true, required: []string{"text", "href"}, props: map[string]prop{"text": {k: kDynString}, "href": {k: kHref}}},
}

// functions are the renderer-side functions the browser implements.
var functions = map[string]bool{"required": true, "regex": true, "length": true, "numeric": true, "email": true, "formatString": true, "formatNumber": true, "formatCurrency": true, "formatDate": true, "pluralize": true, "and": true, "or": true, "not": true, "@index": true}

// Surface is what the browser draws for one surfaceId.
type Surface struct {
	ID         string
	CatalogID  string
	Components map[string]map[string]any
}

// State is every live surface in one conversation.
type State struct {
	Surfaces map[string]*Surface
	order    []string
}

func NewState() *State { return &State{Surfaces: map[string]*Surface{}} }

func (st *State) clone() *State {
	out := &State{Surfaces: make(map[string]*Surface, len(st.Surfaces)), order: append([]string(nil), st.order...)}
	for id, s := range st.Surfaces {
		c := &Surface{ID: s.ID, CatalogID: s.CatalogID, Components: make(map[string]map[string]any, len(s.Components))}
		for k, v := range s.Components {
			c.Components[k] = v
		}
		out.Surfaces[id] = c
	}
	return out
}

// Result summarises what one call changed, for the model.
type Result struct {
	Created, Updated, Deleted []string
	Components                map[string]int
}

// Summary is the tool result the model reads.
func (r Result) Summary() string {
	var b strings.Builder
	part := func(verb string, ids []string) {
		for _, id := range ids {
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			if verb == "Removed" {
				fmt.Fprintf(&b, "Removed surface %q.", id)
			} else {
				fmt.Fprintf(&b, "%s surface %q (%d components).", verb, id, r.Components[id])
			}
		}
	}
	part("Showed", r.Created)
	part("Updated", r.Updated)
	part("Removed", r.Deleted)
	if b.Len() == 0 {
		return "Nothing changed."
	}
	b.WriteString(" The person sees it in the chat now. A button press arrives as their next message, starting with [A2UI action].")
	return b.String()
}

// Messages pulls the message list out of show_ui's arguments: either
// {"messages": [...]} or a bare array.
func Messages(raw []byte) ([]map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return nil, fmt.Errorf("show_ui arguments are not JSON")
	}
	if m, ok := v.(map[string]any); ok {
		v = m["messages"]
		if s, ok := v.(string); ok {
			// Some models send the list as a JSON string.
			d := json.NewDecoder(strings.NewReader(s))
			d.UseNumber()
			if d.Decode(&v) != nil {
				return nil, fmt.Errorf("messages must be an array of A2UI messages")
			}
		}
	}
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("messages must be a non-empty array of A2UI messages")
	}
	if len(list) > MaxMessages {
		return nil, fmt.Errorf("at most %d messages per call", MaxMessages)
	}
	out := make([]map[string]any, 0, len(list))
	for i, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("message %d is not an object", i)
		}
		out = append(out, m)
	}
	return out, nil
}

// Apply checks one show_ui call and applies it. On any error nothing
// changes, so the model can correct the call and send it again.
func (st *State) Apply(raw []byte) (Result, error) {
	msgs, e := Messages(raw)
	if e != nil {
		return Result{}, e
	}
	next := st.clone()
	res := Result{Components: map[string]int{}}
	touched := map[string]bool{}
	created := map[string]bool{}
	for i, m := range msgs {
		if e := next.apply(m, &res, touched, created); e != nil {
			return Result{}, fmt.Errorf("message %d: %w", i, e)
		}
	}
	for id := range touched {
		s, ok := next.Surfaces[id]
		if !ok {
			continue
		}
		if e := s.integrity(); e != nil {
			return Result{}, fmt.Errorf("surface %q: %w", id, e)
		}
		res.Components[id] = len(s.Components)
		if !created[id] {
			res.Updated = append(res.Updated, id)
		}
	}
	for _, id := range next.order {
		if created[id] {
			res.Created = append(res.Created, id)
		}
	}
	sort.Strings(res.Updated)
	if len(next.Surfaces) > MaxSurfaces {
		return Result{}, fmt.Errorf("this chat already shows %d surfaces; remove old ones with deleteSurface or update an existing one", MaxSurfaces)
	}
	*st = *next
	return res, nil
}

// Replay rebuilds a conversation's state from its accepted show_ui calls, in
// order. Calls that no longer apply are skipped.
func Replay(calls []string) *State {
	st := NewState()
	for _, c := range calls {
		_, _ = st.Apply([]byte(c))
	}
	return st
}

func (st *State) apply(m map[string]any, res *Result, touched, created map[string]bool) error {
	if v, ok := m["version"]; ok && v != Version {
		return fmt.Errorf("version must be %q", Version)
	}
	var key string
	for k := range m {
		if k == "version" {
			continue
		}
		if key != "" {
			return fmt.Errorf("each message holds exactly one of createSurface, updateComponents, updateDataModel or deleteSurface")
		}
		key = k
	}
	body, ok := m[key].(map[string]any)
	if !ok {
		if key == "" {
			return fmt.Errorf("empty message")
		}
		return fmt.Errorf("%s must be an object", key)
	}
	id, _ := body["surfaceId"].(string)
	if !idRe.MatchString(id) {
		return fmt.Errorf("%s needs a surfaceId of 1-64 letters, digits, _ . : or -", key)
	}
	switch key {
	case "createSurface":
		if err := only(body, "surfaceId", "catalogId", "components", "dataModel", "sendDataModel", "metadata"); err != nil {
			return fmt.Errorf("createSurface: %w", err)
		}
		if _, ok := st.Surfaces[id]; ok {
			return fmt.Errorf("surface %q already exists (INTEGRITY_ERROR): send updateComponents to change it, or deleteSurface first", id)
		}
		cat, _ := body["catalogId"].(string)
		if cat == "" {
			cat = CatalogID
		}
		if cat != CatalogID && cat != BasicCatalogID {
			return fmt.Errorf("unknown catalogId %q; use %q", cat, CatalogID)
		}
		s := &Surface{ID: id, CatalogID: cat, Components: map[string]map[string]any{}}
		st.Surfaces[id] = s
		st.order = append(st.order, id)
		touched[id], created[id] = true, true
		if dm, ok := body["dataModel"]; ok {
			if _, isObj := dm.(map[string]any); !isObj {
				return fmt.Errorf("createSurface.dataModel must be an object")
			}
			if e := dataSize(dm); e != nil {
				return e
			}
		}
		if cs, ok := body["components"]; ok {
			return s.upsert(cs)
		}
		return nil
	case "updateComponents":
		if err := only(body, "surfaceId", "components"); err != nil {
			return fmt.Errorf("updateComponents: %w", err)
		}
		s, ok := st.Surfaces[id]
		if !ok {
			return fmt.Errorf("surface %q does not exist: send createSurface first", id)
		}
		touched[id] = true
		cs, ok := body["components"]
		if !ok {
			return fmt.Errorf("updateComponents needs components")
		}
		return s.upsert(cs)
	case "updateDataModel":
		if err := only(body, "surfaceId", "path", "value"); err != nil {
			return fmt.Errorf("updateDataModel: %w", err)
		}
		if _, ok := st.Surfaces[id]; !ok {
			return fmt.Errorf("surface %q does not exist: send createSurface first", id)
		}
		if p, ok := body["path"]; ok {
			ps, ok := p.(string)
			if !ok || !pointerOK(ps, true) {
				return fmt.Errorf("updateDataModel.path must be a JSON Pointer such as /order/name")
			}
		}
		v, ok := body["value"]
		if !ok {
			return fmt.Errorf("updateDataModel needs value (null removes the key)")
		}
		touched[id] = true
		return dataSize(v)
	case "deleteSurface":
		if err := only(body, "surfaceId"); err != nil {
			return fmt.Errorf("deleteSurface: %w", err)
		}
		if _, ok := st.Surfaces[id]; !ok {
			return fmt.Errorf("surface %q does not exist", id)
		}
		delete(st.Surfaces, id)
		for i, x := range st.order {
			if x == id {
				st.order = append(st.order[:i], st.order[i+1:]...)
				break
			}
		}
		delete(touched, id)
		if created[id] {
			delete(created, id)
		} else {
			res.Deleted = append(res.Deleted, id)
		}
		return nil
	case "callRendererFunction":
		return fmt.Errorf("callRendererFunction is not supported here")
	}
	return fmt.Errorf("unknown message %q: use createSurface, updateComponents, updateDataModel or deleteSurface", key)
}

func only(m map[string]any, keys ...string) error {
	for k := range m {
		ok := false
		for _, x := range keys {
			ok = ok || k == x
		}
		if !ok {
			return fmt.Errorf("unknown field %q", k)
		}
	}
	return nil
}

func dataSize(v any) error {
	b, _ := json.Marshal(v)
	if len(b) > MaxDataBytes {
		return fmt.Errorf("data model values are limited to %d KiB", MaxDataBytes>>10)
	}
	return nil
}

func (s *Surface) upsert(raw any) error {
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("components must be an array")
	}
	for i, x := range list {
		c, ok := x.(map[string]any)
		if !ok {
			return fmt.Errorf("component %d is not an object", i)
		}
		id, _ := c["id"].(string)
		if !idRe.MatchString(id) {
			return fmt.Errorf("component %d needs an id of 1-64 letters, digits, _ . : or -", i)
		}
		if e := checkComponent(c, s.CatalogID); e != nil {
			return fmt.Errorf("component %q: %w", id, e)
		}
		s.Components[id] = c
	}
	if len(s.Components) > MaxComponents {
		return fmt.Errorf("a surface holds at most %d components", MaxComponents)
	}
	return nil
}

func checkComponent(c map[string]any, catalogID string) error {
	name, _ := c["component"].(string)
	sp, ok := catalog[name]
	if !ok || (sp.custom && catalogID != CatalogID) {
		switch name {
		case "Video", "AudioPlayer":
			return fmt.Errorf("%s is not available in the panel", name)
		case "MultipleChoice":
			return fmt.Errorf("MultipleChoice is ChoicePicker in A2UI v1.0")
		}
		return fmt.Errorf("unknown component %q; the catalog has %s", name, strings.Join(Components(catalogID), ", "))
	}
	for _, r := range sp.required {
		if _, ok := c[r]; !ok {
			return fmt.Errorf("%s needs %s", name, r)
		}
	}
	for k, v := range c {
		switch k {
		case "id", "component":
			continue
		case "weight":
			if _, ok := v.(json.Number); !ok {
				return fmt.Errorf("weight must be a number")
			}
			continue
		case "@catalogId":
			continue
		case "@metadata":
			if _, ok := v.(map[string]any); !ok {
				return fmt.Errorf("@metadata must be an object")
			}
			continue
		case "@accessibility":
			if e := checkKind(v, prop{k: kAccessibility}); e != nil {
				return fmt.Errorf("@accessibility: %w", e)
			}
			continue
		case "@checks":
			if !sp.checkable {
				return fmt.Errorf("%s does not take @checks", name)
			}
			if e := checkKind(v, prop{k: kChecks}); e != nil {
				return fmt.Errorf("@checks: %w", e)
			}
			continue
		}
		p, ok := sp.props[k]
		if !ok {
			if name == "Text" && k == "usageHint" {
				return fmt.Errorf("usageHint is gone in A2UI v1.0: use variant (caption or body), or a Heading component")
			}
			return fmt.Errorf("%s has no property %q", name, k)
		}
		if name == "TextField" && k == "variant" && v == "obscured" {
			return fmt.Errorf("obscured fields are not allowed: values typed here reach the assistant; ask for secrets with request_environment_values")
		}
		if e := checkKind(v, p); e != nil {
			return fmt.Errorf("%s: %w", k, e)
		}
	}
	return nil
}

// Components lists the component names a catalog offers.
func Components(catalogID string) []string {
	var out []string
	for n, sp := range catalog {
		if !sp.custom || catalogID == CatalogID {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func checkKind(v any, p prop) error {
	switch p.k {
	case kString:
		s, ok := v.(string)
		if !ok || len(s) > maxString {
			return fmt.Errorf("must be a string")
		}
	case kNumber:
		if _, ok := v.(json.Number); !ok {
			return fmt.Errorf("must be a number")
		}
	case kInteger:
		n, ok := v.(json.Number)
		if _, e := n.Int64(); !ok || e != nil {
			return fmt.Errorf("must be an integer")
		}
	case kBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("must be true or false")
		}
	case kEnum:
		s, _ := v.(string)
		for _, x := range p.enum {
			if s == x {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(p.enum, ", "))
	case kDynString:
		return dynamic(v, "string")
	case kDynNumber:
		return dynamic(v, "number")
	case kDynBool:
		return dynamic(v, "boolean")
	case kDynStringList:
		return dynamic(v, "stringList")
	case kDynValue:
		return dynamic(v, "any")
	case kChild:
		if s, ok := v.(string); !ok || !idRe.MatchString(s) {
			return fmt.Errorf("must be a component id")
		}
	case kChildList:
		switch t := v.(type) {
		case []any:
			for _, x := range t {
				if s, ok := x.(string); !ok || !idRe.MatchString(s) {
					return fmt.Errorf("must list component ids")
				}
			}
		case map[string]any:
			if e := only(t, "path", "componentId"); e != nil {
				return e
			}
			ps, _ := t["path"].(string)
			cid, _ := t["componentId"].(string)
			if !pointerOK(ps, false) || !idRe.MatchString(cid) {
				return fmt.Errorf("a template needs path (a JSON Pointer string) and componentId")
			}
		default:
			return fmt.Errorf("must be an array of ids or a template {path, componentId}")
		}
	case kTabs:
		list, ok := v.([]any)
		if !ok || len(list) == 0 || len(list) > 12 {
			return fmt.Errorf("must list 1-12 tabs")
		}
		for _, x := range list {
			t, ok := x.(map[string]any)
			if !ok || only(t, "title", "child") != nil {
				return fmt.Errorf("each tab is {title, child}")
			}
			if e := dynamic(t["title"], "string"); e != nil {
				return fmt.Errorf("tab title %w", e)
			}
			if c, ok := t["child"].(string); !ok || !idRe.MatchString(c) {
				return fmt.Errorf("each tab needs a child id")
			}
		}
	case kOptions:
		list, ok := v.([]any)
		if !ok || len(list) == 0 || len(list) > 60 {
			return fmt.Errorf("must list 1-60 options")
		}
		for _, x := range list {
			o, ok := x.(map[string]any)
			if !ok || only(o, "label", "value") != nil {
				return fmt.Errorf("each option is {label, value}")
			}
			if e := dynamic(o["label"], "string"); e != nil {
				return fmt.Errorf("option label %w", e)
			}
			if s, ok := o["value"].(string); !ok || len(s) > 200 {
				return fmt.Errorf("each option needs a string value")
			}
		}
	case kIcon:
		switch t := v.(type) {
		case string:
			for _, x := range Icons {
				if t == x {
					return nil
				}
			}
			return fmt.Errorf("unknown icon %q", t)
		case map[string]any:
			if _, ok := t["@path"]; ok {
				return dynamic(t, "string")
			}
			if only(t, "svgPath") != nil {
				return fmt.Errorf("must be an icon name, {svgPath} or a binding")
			}
			return dynamic(t["svgPath"], "string")
		default:
			return fmt.Errorf("must be an icon name")
		}
	case kImageURL:
		if s, ok := v.(string); ok {
			if !SamePath(s) {
				return fmt.Errorf("images must be panel addresses starting with / (external images are not loaded)")
			}
			return nil
		}
		return dynamic(v, "string")
	case kHref:
		if s, ok := v.(string); ok {
			if !SafeHref(s) {
				return fmt.Errorf("links must be panel paths such as /bots/{id} or https:// addresses")
			}
			return nil
		}
		return dynamic(v, "string")
	case kAction:
		return checkAction(v)
	case kChecks:
		list, ok := v.([]any)
		if !ok || len(list) > 10 {
			return fmt.Errorf("must be an array of at most 10 rules")
		}
		for _, x := range list {
			r, ok := x.(map[string]any)
			if !ok || only(r, "condition", "message") != nil {
				return fmt.Errorf("each rule is {condition, message}")
			}
			cond, ok := r["condition"].(map[string]any)
			if !ok {
				return fmt.Errorf("condition must be a binding or a function call")
			}
			if e := dynamic(cond, "boolean"); e != nil {
				return fmt.Errorf("condition %w", e)
			}
			if m, ok := r["message"]; ok {
				if s, ok := m.(string); !ok || len(s) > 300 {
					return fmt.Errorf("message must be a short string")
				}
			}
		}
	case kAccessibility:
		a, ok := v.(map[string]any)
		if !ok || only(a, "label", "description", "live", "hidden") != nil {
			return fmt.Errorf("takes label, description, live and hidden")
		}
		for k, x := range a {
			switch k {
			case "live":
				if e := checkKind(x, enum("off", "polite", "assertive")); e != nil {
					return e
				}
			case "hidden":
				if e := dynamic(x, "boolean"); e != nil {
					return e
				}
			default:
				if e := dynamic(x, "string"); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func checkAction(v any) error {
	a, ok := v.(map[string]any)
	if !ok || len(a) != 1 {
		return fmt.Errorf("must be {\"event\": {...}} or {\"functionCall\": {...}}")
	}
	if ev, ok := a["event"].(map[string]any); ok {
		if e := only(ev, "name", "context", "userMessage"); e != nil {
			return e
		}
		n, _ := ev["name"].(string)
		if !actionRe.MatchString(n) {
			return fmt.Errorf("event.name must be 1-64 letters, digits, _ . : or -")
		}
		if um, ok := ev["userMessage"]; ok {
			if e := dynamic(um, "string"); e != nil {
				return fmt.Errorf("userMessage %w", e)
			}
		}
		if ctx, ok := ev["context"]; ok {
			m, ok := ctx.(map[string]any)
			if !ok || len(m) > 40 {
				return fmt.Errorf("event.context must be an object of at most 40 keys")
			}
			for k, x := range m {
				if e := dynamic(x, "any"); e != nil {
					return fmt.Errorf("context %s: %w", k, e)
				}
			}
		}
		return nil
	}
	if fc, ok := a["functionCall"].(map[string]any); ok {
		if fc["@call"] != "openUrl" {
			return fmt.Errorf("the only local function a button can call is openUrl")
		}
		if e := only(fc, "@call", "args", "catalogId"); e != nil {
			return e
		}
		args, _ := fc["args"].(map[string]any)
		u, ok := args["url"]
		if !ok || len(args) != 1 {
			return fmt.Errorf("openUrl takes args {url}")
		}
		if s, ok := u.(string); ok {
			if !SafeHref(s) {
				return fmt.Errorf("openUrl opens panel paths or https:// addresses only")
			}
			return nil
		}
		return dynamic(u, "string")
	}
	return fmt.Errorf("must be {\"event\": {...}} or {\"functionCall\": {...}}")
}

// dynamic checks a literal of the wanted type, a {"@path"} binding or a
// {"@call"} function call.
func dynamic(v any, want string) error {
	switch t := v.(type) {
	case nil:
		return fmt.Errorf("is missing")
	case string:
		if len(t) > maxString {
			return fmt.Errorf("is longer than %d characters", maxString)
		}
		if want == "string" || want == "any" {
			return nil
		}
	case json.Number:
		if want == "number" || want == "any" {
			return nil
		}
	case bool:
		if want == "boolean" || want == "any" {
			return nil
		}
	case []any:
		if want == "any" {
			b, _ := json.Marshal(t)
			if len(b) > maxString*2 {
				return fmt.Errorf("is too large")
			}
			return nil
		}
		if want == "stringList" {
			for _, x := range t {
				if _, ok := x.(string); !ok {
					return fmt.Errorf("must be a list of strings")
				}
			}
			return nil
		}
	case map[string]any:
		if p, ok := t["@path"]; ok {
			ps, _ := p.(string)
			if len(t) != 1 || !pointerOK(ps, false) {
				return fmt.Errorf("a binding is exactly {\"@path\": \"/json/pointer\"} (relative paths inside templates)")
			}
			return nil
		}
		if c, ok := t["@call"]; ok {
			name, _ := c.(string)
			if name == "openUrl" {
				return fmt.Errorf("openUrl runs only from a button's action")
			}
			if !functions[name] {
				return fmt.Errorf("unknown function %q", name)
			}
			if e := only(t, "@call", "args", "catalogId"); e != nil {
				return e
			}
			if a, ok := t["args"]; ok {
				am, ok := a.(map[string]any)
				if !ok {
					return fmt.Errorf("%s args must be an object", name)
				}
				for k, x := range am {
					if k == "values" {
						list, ok := x.([]any)
						if !ok {
							return fmt.Errorf("%s values must be an array", name)
						}
						for _, y := range list {
							if e := dynamic(y, "any"); e != nil {
								return e
							}
						}
						continue
					}
					if e := dynamic(x, "any"); e != nil {
						return fmt.Errorf("%s.%s %w", name, k, e)
					}
				}
			}
			return nil
		}
		if want == "any" {
			b, _ := json.Marshal(t)
			if len(b) > maxString*2 {
				return fmt.Errorf("is too large")
			}
			return nil
		}
		return fmt.Errorf("must be a literal, {\"@path\": ...} or {\"@call\": ...}")
	}
	return fmt.Errorf("must be a %s, {\"@path\": ...} or {\"@call\": ...}", strings.Replace(want, "stringList", "list of strings", 1))
}

// pointerOK accepts JSON Pointers; relative ones (no leading slash) resolve
// against a template item.
func pointerOK(p string, absolute bool) bool {
	if len(p) > 200 || strings.ContainsAny(p, "\x00\n") {
		return false
	}
	if absolute {
		return p == "" || strings.HasPrefix(p, "/")
	}
	return p != ""
}

// SamePath accepts a panel path such as /icons/x.svg (never //host or /\host).
func SamePath(s string) bool {
	return strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "/\\") && !strings.ContainsAny(s, " \t\r\n\\") && len(s) <= 500
}

// SafeHref accepts a panel path or an https:// address.
func SafeHref(s string) bool {
	if SamePath(s) {
		return true
	}
	return strings.HasPrefix(s, "https://") && len(s) > len("https://") && !strings.ContainsAny(s, " \t\r\n\\") && len(s) <= 2000
}

// integrity: a root, every referenced child present, and no cycles.
func (s *Surface) integrity() error {
	if _, ok := s.Components["root"]; !ok {
		return fmt.Errorf("needs a component with id \"root\"")
	}
	for id, c := range s.Components {
		for _, ref := range children(c) {
			if _, ok := s.Components[ref]; !ok {
				return fmt.Errorf("component %q refers to %q, which does not exist", id, ref)
			}
		}
	}
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var visit func(id string, depth int) error
	visit = func(id string, depth int) error {
		if depth > 40 {
			return fmt.Errorf("components nest more than 40 levels deep")
		}
		switch color[id] {
		case grey:
			return fmt.Errorf("component %q contains itself", id)
		case black:
			return nil
		}
		color[id] = grey
		for _, ref := range children(s.Components[id]) {
			if e := visit(ref, depth+1); e != nil {
				return e
			}
		}
		color[id] = black
		return nil
	}
	ids := make([]string, 0, len(s.Components))
	for id := range s.Components {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if e := visit(id, 0); e != nil {
			return e
		}
	}
	return nil
}

func children(c map[string]any) []string {
	var out []string
	add := func(v any) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	add(c["child"])
	add(c["trigger"])
	add(c["content"])
	switch t := c["children"].(type) {
	case []any:
		for _, x := range t {
			add(x)
		}
	case map[string]any:
		add(t["componentId"])
	}
	if tabs, ok := c["tabs"].([]any); ok {
		for _, x := range tabs {
			if m, ok := x.(map[string]any); ok {
				add(m["child"])
			}
		}
	}
	return out
}

// Action is a button press the browser reports (A2UI v1.0 "action").
type Action struct {
	Name              string         `json:"name"`
	SurfaceID         string         `json:"surfaceId"`
	SourceComponentID string         `json:"sourceComponentId"`
	Timestamp         string         `json:"timestamp,omitempty"`
	Context           map[string]any `json:"context"`
	UserMessage       string         `json:"userMessage,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

// Envelope is the action as the model reads it: the A2UI v1.0 renderer-to-
// agent message, with the button's label in metadata.
func (a Action) Envelope(label string) string {
	if label != "" {
		a.Metadata = map[string]any{"label": label}
	} else {
		a.Metadata = nil
	}
	if a.Context == nil {
		a.Context = map[string]any{}
	}
	b, _ := json.Marshal(struct {
		Version string `json:"version"`
		Action  Action `json:"action"`
	}{Version, a})
	return ActionPrefix + string(b)
}

// ActionPrefix starts the chat message an action becomes.
const ActionPrefix = "[A2UI action] "

// CheckAction accepts an action only for a button this conversation really
// showed, with the event name that button carries. It returns the button's
// label for the transcript.
func (st *State) CheckAction(a Action) (string, error) {
	s, ok := st.Surfaces[a.SurfaceID]
	if !ok {
		return "", fmt.Errorf("that interface is no longer available")
	}
	c, ok := s.Components[a.SourceComponentID]
	if !ok || c["component"] != "Button" {
		return "", fmt.Errorf("that button is no longer available")
	}
	act, _ := c["action"].(map[string]any)
	ev, _ := act["event"].(map[string]any)
	if ev == nil || ev["name"] != a.Name {
		return "", fmt.Errorf("that button does not send %q", a.Name)
	}
	if a.Context == nil {
		a.Context = map[string]any{}
	}
	b, e := json.Marshal(a.Context)
	if e != nil || len(b) > maxContext {
		return "", fmt.Errorf("the form values are too large")
	}
	if len(a.UserMessage) > 2000 {
		return "", fmt.Errorf("the message is too long")
	}
	label := ""
	if child, ok := s.Components[fmt.Sprint(c["child"])]; ok && child["component"] == "Text" {
		label, _ = child["text"].(string)
	}
	return label, nil
}
