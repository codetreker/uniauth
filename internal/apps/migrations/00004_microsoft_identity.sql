-- +goose Up
UPDATE account_identities
SET provider_id = 'msft:' || substr(provider_id, length('microsoft:9188040d-6c67-4c5b-b112-36a304b66dad:') + 1)
    || '@9188040d-6c67-4c5b-b112-36a304b66dad'
WHERE provider = 'microsoft'
    AND provider_id GLOB 'microsoft:9188040d-6c67-4c5b-b112-36a304b66dad:?*';

-- +goose Down
UPDATE account_identities
SET provider_id = 'microsoft:9188040d-6c67-4c5b-b112-36a304b66dad:'
    || substr(provider_id, length('msft:') + 1,
        length(provider_id) - length('msft:') - length('@9188040d-6c67-4c5b-b112-36a304b66dad'))
WHERE provider = 'microsoft'
    AND provider_id GLOB 'msft:?*@9188040d-6c67-4c5b-b112-36a304b66dad';
