ALTER TABLE image_download_jobs
    ADD COLUMN source text NOT NULL DEFAULT 'official'
    CHECK (source IN ('official', 'tuna'));
