CREATE TABLE digital_archive_parts (
    archive_id INTEGER NOT NULL REFERENCES digital_archives(id),
    part_number INTEGER NOT NULL CHECK(part_number > 0),
    file_id TEXT NOT NULL,
    file_name TEXT NOT NULL,
    offset_bytes INTEGER NOT NULL CHECK(offset_bytes >= 0),
    size_bytes INTEGER NOT NULL CHECK(size_bytes > 0),
    PRIMARY KEY(archive_id, part_number)
);
CREATE TABLE digital_delivery_parts (
    delivery_id INTEGER NOT NULL REFERENCES digital_deliveries(id),
    archive_id INTEGER NOT NULL REFERENCES digital_archives(id),
    part_number INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    PRIMARY KEY(delivery_id, archive_id, part_number)
);

-- Split previously uploaded local ZIPs too. Existing Telegram file IDs stay reusable.
WITH RECURSIVE volumes(archive_id,part_number,file_id,file_name,offset_bytes,size_bytes,total_bytes) AS (
    SELECT id,1,file_id,file_name,0,MIN(size_bytes,49000000),size_bytes
    FROM digital_archives WHERE file_id LIKE 'local:%' AND size_bytes > 50000000
    UNION ALL
    SELECT archive_id,part_number+1,file_id,file_name,offset_bytes+size_bytes,
           MIN(total_bytes-offset_bytes-size_bytes,49000000),total_bytes
    FROM volumes WHERE offset_bytes+size_bytes < total_bytes
)
INSERT INTO digital_archive_parts(archive_id,part_number,file_id,file_name,offset_bytes,size_bytes)
SELECT archive_id,part_number,file_id,printf('%s.%03d',file_name,part_number),offset_bytes,size_bytes FROM volumes;
