# Third-party data notices

## Copernicus DEM GLO-30 Public

The optional static terrain skyline is derived from the 2021 Copernicus DEM
GLO-30 Public digital surface model distributed through the Registry of Open
Data on AWS. Source and licence information:

- <https://registry.opendata.aws/copernicus-dem/>
- <https://dataspace.copernicus.eu/explore-data/data-collections/copernicus-contributing-missions/collections-description/COP-DEM>
- <https://dataspace.copernicus.eu/sites/default/files/media/files/2025-06/copernicus_contributing_mission_data_access_v2_cop_dem_licenses.pdf>

Required notice for the derived skyline:

> produced using Copernicus WorldDEM-30 © DLR e.V. 2010-2014 and © Airbus
> Defence and Space GmbH 2014-2018 provided under COPERNICUS by the European
> Union and ESA; all rights reserved

The organisations in charge of the Copernicus programme by law or by
delegation do not incur any liability for any use of the Copernicus
WorldDEM-30.

The project downloads and caches source COG tiles but does not redistribute
them in Git. Derived profile data retains the source/version/datum digest and
the user-visible Horizon and Astrodome presentations display the required
notice.

The adapter consumes the AWS GLO-30 Public COG layout documented at
<https://copernicus-dem-30m.s3.amazonaws.com/readme.html>. It preserves the
source DGED semantics documented by the Copernicus DEM Product Handbook:
Float32 heights in metres, WGS84 horizontal coordinates, EGM2008 vertical
datum, and `-32767` as NoData. The COG transformation omits optional band
scale/offset/unit tags; Astrosferum therefore records and validates the
canonical unpacked semantics `scale=1`, `offset=0`, `unit=m` explicitly.
