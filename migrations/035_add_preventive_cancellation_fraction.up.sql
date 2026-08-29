ALTER TABLE venues ADD COLUMN preventive_cancellation_fraction TEXT NOT NULL DEFAULT '1/2'
    CHECK (preventive_cancellation_fraction IN ('1/3', '1/2', '2/3'));
