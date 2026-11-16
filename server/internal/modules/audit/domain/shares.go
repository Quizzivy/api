package domain

// ShareField is the diff key under which a grant's entries (share.granted,
// share.changed, share.revoked, share.removed) record its recipient, as
// {"granteeId", "access"}: ids only, because a name frozen in the
// append-only log could never be anonymised (D16).
const ShareField = "shared_with"

// GranteeRefs names the users the shared_with changes of diff record, for the
// read to resolve their names with the rows its entries are about.
func GranteeRefs(diff []byte) []EntityRef {
	var refs []EntityRef
	for _, change := range Present(diff).Changes {
		if change.Field != ShareField {
			continue
		}
		for _, value := range []any{change.From, change.To} {
			if id, _, ok := grantOf(value); ok {
				refs = append(refs, EntityRef{Type: "user", ID: id})
			}
		}
	}
	return refs
}

// NameGrantees shows each recipient of a shared_with change the way the Admin
// deck does, "{name} (can edit)" or "{name} (can use)", from names, the
// users' names by id. A recipient whose name is unknown, an account since
// removed, keeps the ids it was stored with.
func (p *Presentation) NameGrantees(names map[string]string) {
	for i, change := range p.Changes {
		if change.Field == ShareField {
			p.Changes[i].From, p.Changes[i].To = named(change.From, names), named(change.To, names)
		}
	}
}

func named(value any, names map[string]string) any {
	id, access, ok := grantOf(value)
	name, known := names[id]
	if !ok || !known {
		return value
	}
	level := "can use"
	if access == "edit" {
		level = "can edit"
	}
	return name + " (" + level + ")"
}

func grantOf(value any) (id, access string, ok bool) {
	object, isObject := value.(map[string]any)
	if !isObject {
		return "", "", false
	}
	id, _ = object["granteeId"].(string)
	access, _ = object["access"].(string)
	return id, access, id != "" && access != ""
}
