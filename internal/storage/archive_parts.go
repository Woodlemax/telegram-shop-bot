package storage

import (
	"context"
	"fmt"
	"strings"
)

// Leave room below Telegram's 50 MB upload limit.
const ArchivePartBytes int64 = 49_000_000
const MaxAdminArchiveBytes int64 = 512 << 20

type ArchivePart struct {
	Number                 int
	FileID, FileName       string
	OffsetBytes, SizeBytes int64
}

func archiveParts(source, name string, size int64) []ArchivePart {
	if !strings.HasPrefix(source, "local:") || size <= MaxTelegramArchiveUploadBytes {
		return nil
	}
	parts := make([]ArchivePart, 0)
	for offset := int64(0); offset < size; offset += ArchivePartBytes {
		number := len(parts) + 1
		parts = append(parts, ArchivePart{number, source, fmt.Sprintf("%s.%03d", name, number), offset, min(ArchivePartBytes, size-offset)})
	}
	return parts
}

func (s *DigitalArchiveStore) Parts(ctx context.Context, d *DigitalDelivery) ([]ArchivePart, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT part_number,file_id,file_name,offset_bytes,size_bytes FROM digital_archive_parts WHERE archive_id=? ORDER BY part_number`, d.ArchiveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := make([]ArchivePart, 0)
	for rows.Next() {
		var p ArchivePart
		if err = rows.Scan(&p.Number, &p.FileID, &p.FileName, &p.OffsetBytes, &p.SizeBytes); err != nil {
			return nil, err
		}
		parts = append(parts, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		parts = append(parts, ArchivePart{Number: 1, FileID: d.FileID, FileName: d.FileName})
	}
	return parts, nil
}

func (s *DigitalArchiveStore) RememberPartFile(ctx context.Context, archiveID int64, p ArchivePart, fileID string) error {
	if !strings.HasPrefix(p.FileID, "local:") || fileID == "" || strings.ContainsAny(fileID, ":/ \t\r\n") {
		return ErrDigitalArchiveNotReady
	}
	_, err := s.db.ExecContext(ctx, `UPDATE digital_archive_parts SET file_id=? WHERE archive_id=? AND part_number=? AND file_id=?`, fileID, archiveID, p.Number, p.FileID)
	return err
}

// Renew before each bounded upload, so a long multipart delivery cannot be claimed twice.
func (s *DigitalArchiveStore) PreparePart(ctx context.Context, d *DigitalDelivery, number int) (int, error) {
	if d.LeaseToken == "" {
		owned, err := s.Owned(ctx, d.UserID, d.ID)
		if err != nil {
			return 0, err
		}
		if owned.ArchiveID != d.ArchiveID {
			return 0, ErrOrderStatusConflict
		}
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE digital_deliveries SET lease_until=unixepoch()+120 WHERE id=? AND archive_id=? AND lease_token=? AND state='sending' AND EXISTS(SELECT 1 FROM orders o WHERE o.id=digital_deliveries.order_id AND `+digitalEntitlement+`)`, d.ID, d.ArchiveID, d.LeaseToken)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrOrderStatusConflict
	}
	var message int
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(message_id),0) FROM digital_delivery_parts WHERE delivery_id=? AND archive_id=? AND part_number=?`, d.ID, d.ArchiveID, number).Scan(&message)
	return message, err
}

func (s *DigitalArchiveStore) FinishPart(ctx context.Context, d *DigitalDelivery, number, message int) error {
	if d.LeaseToken == "" {
		return nil
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO digital_delivery_parts(delivery_id,archive_id,part_number,message_id) SELECT id,archive_id,?,? FROM digital_deliveries WHERE id=? AND archive_id=? AND lease_token=? AND state='sending' ON CONFLICT(delivery_id,archive_id,part_number) DO UPDATE SET message_id=excluded.message_id`, number, message, d.ID, d.ArchiveID, d.LeaseToken)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrOrderStatusConflict
	}
	return nil
}
