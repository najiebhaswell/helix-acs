package cwmp

import (
	"testing"

	"github.com/raykavin/helix-acs/internal/schema"
	"github.com/stretchr/testify/assert"
)

func TestWANProvision_OnSetParams_LastStepReturnsNil(t *testing.T) {
	flow := &schema.ProvisionFlow{
		ID: "test_flow",
		Steps: []schema.ProvisionStep{
			{
				Kind:   schema.StepSet,
				Params: map[string]string{"Device.PPP.Interface.1.Enable": "1"},
			},
		},
	}
	exe := schema.NewProvisionExecutor(flow, nil, nil)
	p := &WANProvision{exe: exe}

	nextXML, err := p.onSetParams()
	assert.NoError(t, err)
	assert.Nil(t, nextXML, "last set step should complete without building next XML")
	assert.True(t, p.done())
}
