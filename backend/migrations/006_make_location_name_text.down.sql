alter table public.locations
    alter column location_name type varchar(255) using location_name::varchar(255);