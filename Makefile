.PHONY: doctor bootstrap-trial bootstrap-mock up-minimal up-full test test-integration test-e2e scenario scenario-suite acceptance down clean-generated

define NOT_READY
	@printf 'not yet implemented: $(1)\n' >&2
	@exit 1
endef

doctor:
	$(call NOT_READY,check local dependencies)
bootstrap-trial:
	$(call NOT_READY,bootstrap trial mode)
bootstrap-mock:
	$(call NOT_READY,bootstrap nvml-mock mode)
up-minimal:
	$(call NOT_READY,start minimal deployment)
up-full:
	$(call NOT_READY,start full deployment)
test:
	$(call NOT_READY,run unit tests)
test-integration:
	$(call NOT_READY,run integration tests)
test-e2e:
	$(call NOT_READY,run end-to-end tests)
scenario:
	$(call NOT_READY,run one deterministic scenario)
scenario-suite:
	$(call NOT_READY,run the deterministic scenario suite)
acceptance:
	$(call NOT_READY,run the acceptance bundle)
down:
	$(call NOT_READY,stop deployments)
clean-generated:
	$(call NOT_READY,clean generated artifacts)
