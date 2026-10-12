"""Add virtual-ROM columns to roms.

`is_virtual` and `source_url` are declared in `utils.roms_columns`, which adds
them in the single table copy shared by every revision that widens `roms`. A
virtual ROM is a file-less row sourced from an archive.org index; it carries
metadata like a physical game and keeps the URL to fetch the game from, and
stops being virtual once the file is downloaded.

Revision ID: 0154_virtual_roms
Revises: 0153_play_permissions
Create Date: 2026-10-12 00:00:00.000000

"""

from alembic import op

from utils.roms_columns import ensure_roms_columns

# revision identifiers, used by Alembic.
revision = "0154_virtual_roms"
down_revision = "0153_play_permissions"
branch_labels = None
depends_on = None


def upgrade() -> None:
    ensure_roms_columns(op.get_bind())


def downgrade() -> None:
    with op.batch_alter_table("roms", schema=None) as batch_op:
        batch_op.drop_column("source_url", if_exists=True)
        batch_op.drop_column("is_virtual", if_exists=True)
