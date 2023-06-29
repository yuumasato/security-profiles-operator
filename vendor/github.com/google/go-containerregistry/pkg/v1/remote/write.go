		defer func() { o.progress.Close(rerr) }()
	}
	return newPusher(o).Push(o.context, ref, ii)
}

// WriteLayer uploads the provided Layer to the specified repo.
func WriteLayer(repo name.Repository, layer v1.Layer, options ...Option) (rerr error) {
	o, err := makeOptions(repo, options...)
	if err != nil {
		return err
	}
	if o.progress != nil {
		defer func() { o.progress.Close(rerr) }()
	}
	return newPusher(o).Upload(o.context, repo, layer)
}

// Tag adds a tag to the given Taggable via PUT /v2/.../manifests/<tag>
//
// Notable implementations of Taggable are v1.Image, v1.ImageIndex, and
// remote.Descriptor.
//
// If t implements MediaType, we will use that for the Content-Type, otherwise
// we will default to types.DockerManifestSchema2.
//
// Tag does not attempt to write anything other than the manifest, so callers
// should ensure that all blobs or manifests that are referenced by t exist
// in the target registry.
func Tag(tag name.Tag, t Taggable, options ...Option) error {
	return Put(tag, t, options...)
}

// Put adds a manifest from the given Taggable via PUT /v1/.../manifest/<ref>
//
// Notable implementations of Taggable are v1.Image, v1.ImageIndex, and
// remote.Descriptor.
//
// If t implements MediaType, we will use that for the Content-Type, otherwise
// we will default to types.DockerManifestSchema2.
//
// Put does not attempt to write anything other than the manifest, so callers
// should ensure that all blobs or manifests that are referenced by t exist
// in the target registry.
func Put(ref name.Reference, t Taggable, options ...Option) error {
	o, err := makeOptions(options...)
	if err != nil {
		return err
	}
	return newPusher(o).Push(o.context, ref, t)
}
