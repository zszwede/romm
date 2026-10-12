"""Add virtual_rom_sources table

An archive.org index assigned to a platform; syncing it creates one virtual
`Rom` per game it lists.

Revision ID: 0155_virtual_rom_sources
Revises: 0154_virtual_roms
Create Date: 2026-10-12 00:00:00.000000

"""

import sqlalchemy as sa
from alembic import op

from utils.database import CustomJSON

# revision identifiers, used by Alembic.
revision = "0155_virtual_rom_sources"
down_revision = "0154_virtual_roms"
branch_labels = None
depends_on = None

INDEX_NAME = "ix_virtual_rom_sources_platform_id"


def upgrade() -> None:
    op.create_table(
        "virtual_rom_sources",
        sa.Column("id", sa.Integer(), autoincrement=True, nullable=False),
        sa.Column("platform_id", sa.Integer(), nullable=False),
        sa.Column("url", sa.String(length=1024), nullable=False),
        sa.Column("kind", sa.String(length=16), nullable=True),
        sa.Column("region_filter", CustomJSON(), nullable=True),
        sa.Column(
            "enabled", sa.Boolean(), nullable=False, server_default=sa.true()
        ),
        sa.Column("last_synced_at", sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column("last_result", sa.Text(), nullable=True),
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
        if_not_exists=True,
    )
    op.create_index(
        INDEX_NAME,
        "virtual_rom_sources",
        ["platform_id"],
        unique=False,
        if_not_exists=True,
    )


def downgrade() -> None:
    op.drop_table("virtual_rom_sources", if_exists=True)
