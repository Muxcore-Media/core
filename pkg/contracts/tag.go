package contracts

import "context"

// Tag is a label that can be applied to any resource for filtering and organization.
type Tag struct {
	ID    string
	Label string
}

// TaggableResource identifies what a tag is attached to.
type TaggableResource struct {
	ResourceID   string // e.g., media object ID, profile ID, import list ID
	ResourceType string // e.g., "media_object", "quality_profile", "import_list"
}

// TagProvider is implemented by a module that stores and manages tags (e.g., tagger).
type TagProvider interface {
	// Create makes a new tag.
	Create(ctx context.Context, label string) (Tag, error)
	// Delete removes a tag and all its associations.
	Delete(ctx context.Context, id string) error
	// Get returns a tag by ID.
	Get(ctx context.Context, id string) (Tag, error)
	// Find returns all tags whose labels contain the query string.
	Find(ctx context.Context, query string) ([]Tag, error)
	// List returns all tags.
	List(ctx context.Context) ([]Tag, error)
	// Apply attaches a tag to one or more resources.
	Apply(ctx context.Context, tagID string, resources []TaggableResource) error
	// Remove detaches a tag from one or more resources.
	Remove(ctx context.Context, tagID string, resources []TaggableResource) error
	// Resources returns all resources that have the given tag.
	Resources(ctx context.Context, tagID string) ([]TaggableResource, error)
	// Tags returns all tags attached to the given resource.
	Tags(ctx context.Context, resource TaggableResource) ([]Tag, error)
}
