package sqlstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/raintank/raintank-apps/worldping-api/model"
)

type endpointRow struct {
	model.Endpoint    `xorm:"extends"`
	model.Check       `xorm:"extends"`
	model.EndpointTag `xorm:"extends"`
}

type endpointRows []*endpointRow

func (endpointRows) TableName() string {
	return "endpoint"
}

func (rows endpointRows) ToDTO() []*model.EndpointDTO {
	endpointsById := make(map[int64]*model.EndpointDTO)
	endpointChecksById := make(map[int64]map[int64]*model.CheckDTO)
	endpointTagsById := make(map[int64]map[string]struct{})
	for _, r := range rows {
		_, ok := endpointsById[r.Endpoint.Id]

		check := &model.CheckDTO{
			Id:             r.Check.Id,
			Type:           r.Check.Type,
			Frequency:      r.Check.Frequency,
			Enabled:        r.Check.Enabled,
			State:          r.Check.State,
			StateCheck:     r.Check.StateCheck,
			StateChange:    r.Check.StateChange,
			Settings:       r.Check.Settings,
			HealthSettings: r.Check.HealthSettings,
			Created:        r.Check.Created,
			Updated:        r.Check.Updated,
		}
		if !ok {
			endpointsById[r.Endpoint.Id] = &model.EndpointDTO{
				Id:      r.Endpoint.Id,
				Owner:   r.Endpoint.Owner,
				Name:    r.Endpoint.Name,
				Slug:    r.Endpoint.Slug,
				Checks:  make([]*model.CheckDTO, 0),
				Tags:    make([]string, 0),
				Created: r.Endpoint.Created,
				Updated: r.Endpoint.Updated,
			}
			endpointChecksById[r.Endpoint.Id] = make(map[int64]*model.CheckDTO)
			endpointTagsById[r.Endpoint.Id] = make(map[string]struct{})
			if check.Id != 0 {
				endpointChecksById[r.Endpoint.Id][check.Id] = check
			}
			if r.EndpointTag.Tag != "" {
				endpointTagsById[r.Endpoint.Id][r.EndpointTag.Tag] = struct{}{}
			}
		} else {
			if check.Id != 0 {
				_, ecOk := endpointChecksById[r.Endpoint.Id][check.Id]
				if !ecOk {
					endpointChecksById[r.Endpoint.Id][check.Id] = check
				}
			}
			if r.EndpointTag.Tag != "" {
				_, tagOk := endpointTagsById[r.Endpoint.Id][r.EndpointTag.Tag]
				if !tagOk {
					endpointTagsById[r.Endpoint.Id][r.EndpointTag.Tag] = struct{}{}
				}
			}
		}
	}
	endpoints := make([]*model.EndpointDTO, len(endpointsById))
	i := 0
	for _, e := range endpointsById {
		for _, c := range endpointChecksById[e.Id] {
			e.Checks = append(e.Checks, c)
		}

		for t, _ := range endpointTagsById[e.Id] {
			e.Tags = append(e.Tags, t)
		}

		endpoints[i] = e
		i++
	}
	return endpoints
}

func GetEndpoints(query *model.GetEndpointsQuery) ([]*model.EndpointDTO, error) {
	sess, err := newSession(false, "endpoint")
	if err != nil {
		return nil, err
	}
	return getEndpoints(sess, query)
}

func getEndpoints(sess *session, query *model.GetEndpointsQuery) ([]*model.EndpointDTO, error) {
	var e endpointRows
	if query.Name != "" {
		sess.Where("endpoint.name like ?", query.Name)
	}
	if query.Tag != "" {
		sess.Join("INNER", []string{"endpoint_tag", "et"}, "endpoint.id = et.endpoint_id").Where("et.tag=?", query.Tag)
	}
	if query.OrderBy == "" {
		query.OrderBy = "name"
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	if query.Page == 0 {
		query.Page = 1
	}
	sess.Asc(query.OrderBy).Limit(query.Limit, (query.Page-1)*query.Limit)

	sess.Join("LEFT", "check", "endpoint.id = `check`.endpoint_id")
	sess.Join("LEFT", "endpoint_tag", "endpoint.id = endpoint_tag.endpoint_id")

	err := sess.Find(&e)
	if err != nil {
		return nil, err
	}
	return e.ToDTO(), nil
}

func GetEndpointById(id int64, owner int64) (*model.EndpointDTO, error) {
	sess, err := newSession(false, "endpoint")
	if err != nil {
		return nil, err
	}
	return getEndpointById(sess, id, owner)
}

func getEndpointById(sess *session, id int64, owner int64) (*model.EndpointDTO, error) {
	var e endpointRows
	sess.Where("endpoint.id=? AND endpoint.owner=?", id, owner)
	sess.Join("LEFT", "check", "endpoint.id = `check`.endpoint_id")
	sess.Join("LEFT", "endpoint_tag", "endpoint.id = endpoint_tag.endpoint_id")

	err := sess.Find(&e)
	if err != nil {
		return nil, err
	}
	if len(e) == 0 {
		return nil, nil
	}
	return e.ToDTO()[0], nil
}

func AddEndpoint(e *model.EndpointDTO) error {
	sess, err := newSession(true, "endpoint")
	if err != nil {
		return err
	}
	defer sess.Cleanup()

	if err = addEndpoint(sess, e); err != nil {
		return err
	}
	sess.Complete()
	return nil
}

func addEndpoint(sess *session, e *model.EndpointDTO) error {
	endpoint := &model.Endpoint{
		Owner:   e.Owner,
		Name:    e.Name,
		Created: time.Now(),
		Updated: time.Now(),
	}
	endpoint.UpdateSlug()
	if _, err := sess.Insert(endpoint); err != nil {
		return err
	}
	e.Id = endpoint.Id
	e.Created = endpoint.Created
	e.Updated = endpoint.Updated
	e.Slug = endpoint.Slug

	endpointTags := make([]model.EndpointTag, 0, len(e.Tags))
	for _, tag := range e.Tags {
		endpointTags = append(endpointTags, model.EndpointTag{
			Owner:      e.Owner,
			EndpointId: endpoint.Id,
			Tag:        tag,
			Created:    time.Now(),
		})
	}
	if len(endpointTags) > 0 {
		sess.Table("endpoint_tag")
		if _, err := sess.Insert(&endpointTags); err != nil {
			return err
		}
	}

	checks := make([]*model.Check, len(e.Checks))
	for i, c := range e.Checks {
		checks[i] = c.ToCheck(e.Owner, e.Id)
		checks[i].State = -1
		checks[i].StateChange = time.Now()
		checks[i].Updated = time.Now()
		checks[i].Created = time.Now()
	}
	if len(checks) > 0 {
		sess.Table("check")
		//perform each insert on its own so that the ID field gets assigned.
		for i, c := range checks {
			if _, err := sess.Insert(c); err != nil {
				return err
			}
			e.Checks[i] = c.ToCheckDTO()
		}
	}

	return nil
}

func UpdateEndpoint(e *model.EndpointDTO) error {
	sess, err := newSession(true, "endpoint")
	if err != nil {
		return err
	}
	defer sess.Cleanup()

	if err = updateEndpoint(sess, e); err != nil {
		return err
	}
	sess.Complete()
	return nil
}

func updateEndpoint(sess *session, e *model.EndpointDTO) error {
	existing, err := getEndpointById(sess, e.Id, e.Owner)
	if err != nil {
		return err
	}
	if existing == nil {
		return model.ErrEndpointNotFound
	}
	endpoint := &model.Endpoint{
		Id:      e.Id,
		Owner:   e.Owner,
		Name:    e.Name,
		Created: existing.Created,
		Updated: time.Now(),
	}
	endpoint.UpdateSlug()
	if _, err := sess.Id(endpoint.Id).Update(endpoint); err != nil {
		return err
	}

	e.Slug = endpoint.Slug
	e.Updated = endpoint.Updated

	/***** Update Tags **********/

	tagMap := make(map[string]bool)
	tagsToDelete := make([]string, 0)
	tagsToAddMap := make(map[string]bool, 0)
	// create map of current tags
	for _, t := range existing.Tags {
		tagMap[t] = false
	}

	// create map of tags to add. We use a map
	// to ensure that we only add each tag once.
	for _, t := range e.Tags {
		if _, ok := tagMap[t]; !ok {
			tagsToAddMap[t] = true
		}
		// mark that this tag has been seen.
		tagMap[t] = true
	}

	//create list of tags to delete
	for t, seen := range tagMap {
		if !seen {
			tagsToDelete = append(tagsToDelete, t)
		}
	}

	// create list of tags to add.
	tagsToAdd := make([]string, len(tagsToAddMap))
	i := 0
	for t := range tagsToAddMap {
		tagsToAdd[i] = t
		i += 1
	}
	if len(tagsToDelete) > 0 {
		rawParams := make([]interface{}, 0)
		rawParams = append(rawParams, e.Id, e.Owner)
		p := make([]string, len(tagsToDelete))
		for i, t := range tagsToDelete {
			p[i] = "?"
			rawParams = append(rawParams, t)
		}
		rawSql := fmt.Sprintf("DELETE FROM endpoint_tag WHERE endpoint_id=? AND owner=? AND tag IN (%s)", strings.Join(p, ","))
		if _, err := sess.Exec(rawSql, rawParams...); err != nil {
			return err
		}
	}
	if len(tagsToAdd) > 0 {
		newEndpointTags := make([]model.EndpointTag, len(tagsToAdd))
		for i, tag := range tagsToAdd {
			newEndpointTags[i] = model.EndpointTag{
				Owner:      e.Owner,
				EndpointId: e.Id,
				Tag:        tag,
				Created:    time.Now(),
			}
		}
		sess.Table("endpoint_tag")
		if _, err := sess.Insert(&newEndpointTags); err != nil {
			return err
		}
	}

	/***** Update Checks **********/
	updatedChecks := make([]*model.CheckDTO, 0, len(e.Checks))

	checkUpdates := make([]*model.Check, 0)
	checkAdds := make([]*model.Check, 0)
	checkDeletes := make([]*model.CheckDTO, 0)

	checkMap := make(map[model.CheckType]*model.CheckDTO)
	seenChecks := make(map[model.CheckType]bool)
	for _, c := range existing.Checks {
		checkMap[c.Type] = c
	}
	for _, c := range e.Checks {
		seenChecks[c.Type] = true
		ec, ok := checkMap[c.Type]
		if !ok {
			check := c.ToCheck(e.Owner, e.Id)
			check.State = -1
			check.StateCheck = time.Now()
			check.Created = time.Now()
			check.Updated = time.Now()
			checkAdds = append(checkAdds, check)
		} else if c.Id == ec.Id {
			cjson, err := json.Marshal(c)
			if err != nil {
				return err
			}
			ecjson, err := json.Marshal(ec)
			if !bytes.Equal(ecjson, cjson) {
				check := c.ToCheck(e.Owner, e.Id)
				check.Updated = time.Now()
				check.Created = ec.Created
				checkUpdates = append(checkAdds, check)
			} else {
				updatedChecks = append(updatedChecks, c)
			}
		} else {
			checkDeletes = append(checkDeletes, ec)
			check := c.ToCheck(e.Owner, e.Id)
			check.State = -1
			check.StateCheck = time.Now()
			check.Created = time.Now()
			check.Updated = time.Now()
			checkAdds = append(checkAdds, check)
		}

		for t, ec := range checkMap {
			if _, ok := seenChecks[t]; !ok {
				checkDeletes = append(checkDeletes, ec)
			}
		}
	}

	if len(checkDeletes) > 0 {
		ids := make([]int64, len(checkDeletes))
		for i, c := range checkDeletes {
			ids[i] = c.Id
		}
		sess.Table("check")
		sess.In("id", ids)
		if _, err := sess.Delete(nil); err != nil {
			return err
		}
	}
	if len(checkAdds) > 0 {
		sess.Table("check")
		sess.UseBool("enabled")
		for _, c := range checkAdds {
			if _, err := sess.Insert(c); err != nil {
				return err
			}
			updatedChecks = append(updatedChecks, c.ToCheckDTO())
		}
	}
	if len(checkUpdates) > 0 {
		sess.Table("check")
		for _, c := range checkUpdates {
			sess.UseBool("enabled")
			if _, err := sess.Id(c.Id).Update(c); err != nil {
				return err
			}
			updatedChecks = append(updatedChecks, c.ToCheckDTO())
		}
	}
	e.Checks = updatedChecks

	return nil
}

func DeleteEndpoint(id, owner int64) error {
	sess, err := newSession(true, "endpoint")
	if err != nil {
		return err
	}
	defer sess.Cleanup()

	if err = deleteEndpoint(sess, id, owner); err != nil {
		return err
	}
	sess.Complete()
	return nil
}

func deleteEndpoint(sess *session, id, owner int64) error {
	existing, err := getEndpointById(sess, id, owner)
	if err != nil {
		return err
	}
	if existing == nil {
		return model.ErrEndpointNotFound
	}
	var rawSql = "DELETE FROM endpoint WHERE id=? and owner=?"
	_, err = sess.Exec(rawSql, id, owner)
	if err != nil {
		return err
	}

	rawSql = "DELETE FROM endpoint_tag WHERE endpoint_id=? and owner=?"
	if _, err := sess.Exec(rawSql, id, owner); err != nil {
		return err
	}

	rawSql = "DELETE FROM `check` WHERE endpoint_id=? and owner=?"
	if _, err := sess.Exec(rawSql, id, owner); err != nil {
		return err
	}
	return nil
}
