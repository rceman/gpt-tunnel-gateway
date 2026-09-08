package sqlitestore

import "errors"

func (d *Databases) SharedPath() string { return d.sharedPath }

func (d *Databases) LocalPath() string { return d.localPath }

func (d *Databases) Close() error {
	if d == nil {
		return nil
	}
	var localErr, sharedErr error
	if d.Local != nil {
		localErr = d.Local.Close()
	}
	if d.Shared != nil {
		sharedErr = d.Shared.Close()
	}
	return errors.Join(localErr, sharedErr)
}
