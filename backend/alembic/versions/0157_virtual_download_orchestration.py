"""virtual download orchestration

Adds the columns and table behind the virtual-ROM download workflow:

* ``roms.virtual_contents`` — the full downloadable set for a virtual game
  (base game + updates + DLC), each ``{category, name, url, host, version,
  size}``. One Rom still represents one game; this holds the pieces a merge
  service needs to fetch and combine into a single file.
* ``roms.source_hosts`` — the distinct hosts a virtual game can be fetched from
  (e.g. ``["1fichier.com", "archive.org"]``), derived from its source URLs and
  contents, stored so the gallery can filter/facet by host without parsing URLs
  at query time.
* ``roms.download_status`` — where a virtual game is in the download pipeline:
  NULL/``none`` (not requested), ``wanted`` (flagged), ``queued``/``running``
  (handed to the merge service), ``done``, ``failed``.
* ``virtual_exclusions`` — games removed with "delete + exclude" so a later
  list re-import never recreates them (matched by platform + normalised name).

Revision ID: 0157_virtual_downloads
Revises: 0156_virtual_rom_mirrors
Create Date: 2026-10-21 00:00:00.000000

"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic. Kept <=32 chars for
# alembic_version.version_num (VARCHAR(32)).
revision = "0157_virtual_downloads"
down_revision = "0156_virtual_rom_mirrors"
branch_labels = None
depends_on = None


def _json():
    return sa.JSON().with_variant(postgresql.JSONB(astext_type=sa.Text()), "postgresql")


def upgrade() -> None:
    with op.batch_alter_table("roms", schema=None) as batch_op:
        batch_op.add_column(
            sa.Column("virtual_contents", _json(), nullable=True),
            if_not_exists=True,
        )
        batch_op.add_column(
            sa.Column("source_hosts", _json(), nullable=True),
            if_not_exists=True,
        )
        batch_op.add_column(
            sa.Column("download_status", sa.String(length=16), nullable=True),
            if_not_exists=True,
        )

    op.create_table(
        "virtual_exclusions",
        sa.Column("id", sa.Integer(), autoincrement=True, nullable=False),
        sa.Column("platform_id", sa.Integer(), nullable=False),
        # The normalised game name (fs_name without extension) that must not be
        # recreated on a future import.
        sa.Column("name", sa.String(length=512), nullable=False),
        sa.Column(
            "created_at",
            sa.TIMESTAMP(timezone=True),
            nullable=False,
            server_default=sa.text("now()"),
        ),
        sa.Column(
            "updated_at",
            sa.TIMESTAMP(timezone=True),
            nullable=False,
            server_default=sa.text("now()"),
        ),
        sa.ForeignKeyConstraint(["platform_id"], ["platforms.id"], ondelete="CASCADE"),
        sa.PrimaryKeyConstraint("id"),
        sa.UniqueConstraint("platform_id", "name", name="uq_virtual_exclusion"),
        if_not_exists=True,
    )
    op.create_index(
        "ix_virtual_exclusions_platform_id",
        "virtual_exclusions",
        ["platform_id"],
        unique=False,
        if_not_exists=True,
    )


def downgrade() -> None:
    op.drop_table("virtual_exclusions", if_exists=True)
    with op.batch_alter_table("roms", schema=None) as batch_op:
        batch_op.drop_column("download_status", if_exists=True)
        batch_op.drop_column("source_hosts", if_exists=True)
        batch_op.drop_column("virtual_contents", if_exists=True)
