package sqlstore

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/raintank/raintank-apps/server/model"
)

type agentWithTag struct {
	model.Agent `xorm:"extends"`
	Tag         string
}

type agentWithTags []*agentWithTag

func (agentWithTags) TableName() string {
	return "agent"
}

func (rows agentWithTags) ToAgentDTO() []*model.AgentDTO {
	agentsById := make(map[int64]*model.AgentDTO)
	for _, r := range rows {
		a, ok := agentsById[r.Id]
		if !ok {
			agentsById[r.Id] = &model.AgentDTO{
				Id:       r.Id,
				Name:     r.Name,
				Password: r.Password,
				Enabled:  r.Enabled,
				Owner:    r.Owner,
				Public:   r.Public,
				Created:  r.Created,
				Updated:  r.Updated,
				Tags:     []string{r.Tag},
			}
		} else {
			a.Tags = append(a.Tags, r.Tag)
		}
	}
	agents := make([]*model.AgentDTO, len(agentsById))
	i := 0
	for _, a := range agentsById {
		agents[i] = a
		i++
	}
	return agents
}

func GetAgents(query *model.GetAgentsQuery) ([]*model.AgentDTO, error) {
	sess, err := newSession(false, "agent")
	if err != nil {
		return nil, err
	}
	return getAgents(sess, query)
}

func getAgents(sess *session, query *model.GetAgentsQuery) ([]*model.AgentDTO, error) {
	var a agentWithTags
	if query.Name != "" {
		sess.Where("agent.name = ?", query.Name)
	}
	if query.Enabled != "" {
		enabled, err := strconv.ParseBool(query.Enabled)
		if err != nil {
			return nil, err
		}
		sess.Where("agent.enabled=?", enabled)
	}
	if query.Public != "" {
		public, err := strconv.ParseBool(query.Public)
		if err != nil {
			return nil, err
		}
		sess.Where("agent.public=?", public)
	}
	if query.Tag != "" {
		sess.Join("INNER", []string{"agent_tag", "at"}, "agent.id = at.agent_id").Where("at.tag=?", query.Tag)
	}
	sess.Cols(
		"agent.id",
		"agent.name",
		"agent.password",
		"agent.enabled",
		"agent.public",
		"agent.created",
		"agent.updated",
		"agent.owner",
		"agent_tag.tag",
	)
	err := sess.Join("INNER", "agent_tag", "agent.id = agent_tag.agent_id").Find(&a)
	if err != nil {
		return nil, err
	}
	return a.ToAgentDTO(), nil
}

func GetAgentById(id int64, owner string) (*model.AgentDTO, error) {
	sess, err := newSession(false, "agent")
	if err != nil {
		return nil, err
	}
	return getAgentById(sess, id, owner)
}

func getAgentById(sess *session, id int64, owner string) (*model.AgentDTO, error) {
	var a agentWithTags
	err := sess.Where("agent.id=?", id).Join("INNER", "agent_tag", "agent.id = agent_tag.agent_id").Find(&a)
	if err != nil {
		return nil, err
	}
	if len(a) == 0 {
		return nil, nil
	}
	return a.ToAgentDTO()[0], nil
}

func UpdateAgent(a *model.AgentDTO) (*model.AgentDTO, error) {
	sess, err := newSession(true, "agent")
	if err != nil {
		return nil, err
	}
	defer sess.Cleanup()

	/*-------- Update existing Agent ---------*/
	if a.Id != 0 {
		existing, err := getAgentById(sess, a.Id, a.Owner)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			// Update existing Agent.
			agent, err := updateAgent(sess, a, existing)
			if err != nil {
				return nil, err
			}
			sess.Complete()
			return agent, err
		}
	}

	/*--------- create new Agent -------------*/
	agent := &model.Agent{
		Name:     a.Name,
		Password: a.Password,
		Enabled:  a.Enabled,
		Owner:    a.Owner,
		Public:   a.Public,
		Created:  time.Now(),
		Updated:  time.Now(),
	}

	sess.UseBool("public")
	sess.UseBool("enabled")
	agent.UpdateSlug()
	if _, err := sess.Insert(agent); err != nil {
		return nil, err
	}
	a.Id = agent.Id

	agentTags := make([]model.AgentTag, 0, len(a.Tags))
	for _, tag := range a.Tags {
		agentTags = append(agentTags, model.AgentTag{
			Owner:   a.Owner,
			AgentId: agent.Id,
			Tag:     tag,
			Created: time.Now(),
		})
	}
	if len(agentTags) > 0 {
		sess.Table("agent_tag")
		if _, err := sess.Insert(&agentTags); err != nil {
			return nil, err
		}
	}
	sess.Complete()
	return a, nil
}

func updateAgent(sess *session, a *model.AgentDTO, existing *model.AgentDTO) (*model.AgentDTO, error) {
	// If the Owner is different, the only changes that can be made is to Tags.
	if a.Owner == existing.Owner {
		agent := &model.Agent{
			Id:       a.Id,
			Name:     a.Name,
			Password: a.Password,
			Enabled:  a.Enabled,
			Owner:    a.Owner,
			Public:   a.Public,
			Created:  a.Created,
			Updated:  time.Now(),
		}
		sess.UseBool("public")
		sess.UseBool("enabled")
		agent.UpdateSlug()
		if _, err := sess.Id(agent.Id).Update(agent); err != nil {
			return nil, err
		}
	}

	tagMap := make(map[string]bool)
	tagsToDelete := make([]string, 0)
	tagsToAddMap := make(map[string]bool, 0)
	// create map of current tags
	for _, t := range existing.Tags {
		tagMap[t] = false
	}

	// create map of tags to add. We use a map
	// to ensure that we only add each tag once.
	for _, t := range a.Tags {
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
		rawParams = append(rawParams, a.Id, a.Owner)
		p := make([]string, len(tagsToDelete))
		for i, t := range tagsToDelete {
			p[i] = "?"
			rawParams = append(rawParams, t)
		}
		rawSql := fmt.Sprintf("DELETE FROM agent_tag WHERE agent_id=? AND owner=? AND tag IN (%s)", strings.Join(p, ","))
		if _, err := sess.Exec(rawSql, rawParams...); err != nil {
			return nil, err
		}
	}
	if len(tagsToAdd) > 0 {
		newAgentTags := make([]model.AgentTag, len(tagsToAdd))
		for i, tag := range tagsToAdd {
			newAgentTags[i] = model.AgentTag{
				Owner:   a.Owner,
				AgentId: a.Id,
				Tag:     tag,
				Created: time.Now(),
			}
		}
		sess.Table("agent_tag")
		if _, err := sess.Insert(&newAgentTags); err != nil {
			return nil, err
		}
	}

	return a, nil
}
