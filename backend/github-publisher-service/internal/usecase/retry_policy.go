package usecase

import (
	"time"

	"backend-github-publisher-service/internal/domain"
)

type RetryPolicyResolver struct {
	defaultPolicy domain.RetryPolicy
}

func NewRetryPolicyResolver(defaultPolicy domain.RetryPolicy) RetryPolicyResolver {
	if defaultPolicy.MaxAttempts <= 0 {
		defaultPolicy.MaxAttempts = 3
	}
	if defaultPolicy.InitialBackoff <= 0 {
		defaultPolicy.InitialBackoff = 5 * time.Second
	}
	if defaultPolicy.MaxBackoff <= 0 {
		defaultPolicy.MaxBackoff = 30 * time.Second
	}
	return RetryPolicyResolver{defaultPolicy: defaultPolicy}
}

func (r RetryPolicyResolver) Resolve(policy *domain.RetryPolicy) domain.RetryPolicy {
	resolved := r.defaultPolicy
	if policy != nil {
		if policy.InitialBackoff > 0 {
			resolved.InitialBackoff = policy.InitialBackoff
		}
		if policy.MaxBackoff > 0 {
			resolved.MaxBackoff = policy.MaxBackoff
		}
		if policy.MaxAttempts > 0 {
			resolved.MaxAttempts = policy.MaxAttempts
		}
	}

	if resolved.MaxAttempts <= 0 {
		resolved.MaxAttempts = r.defaultPolicy.MaxAttempts
	}
	if resolved.InitialBackoff <= 0 {
		resolved.InitialBackoff = r.defaultPolicy.InitialBackoff
	}
	if resolved.MaxBackoff <= 0 {
		resolved.MaxBackoff = r.defaultPolicy.MaxBackoff
	}

	return resolved
}
