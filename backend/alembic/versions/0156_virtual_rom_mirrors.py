"""Add virtual_rom_mirrors table

Extra download locations for a virtual ROM, so a game listed by two sources on
the same platform is one row with several mirrors (also used as download
fallbacks). A side table avoids another full copy of the roms table.

Revision ID: 0156_virtual_rom_mirrors
Revises: 0155_virtual_rom_sources
Create Date: 2026-10-13 00:00:00.000000

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = "0156_virtual_rom_mirrors"
down_revision = "0155_virtual_rom_sources"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "virtual_rom_mirrors",
        sa.Column("id", sa.Integer(), autoincrement=True, nullable=False),
        sa.Column("rom_id", sa.Integer(), nullable=False),
        sa.Column("url", sa.String(length=1024), nullable=False),
        sa.Column("source_id", sa.Integer(), nullable=True),
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
        sa.ForeignKeyConstraint(["rom_id"], ["roms.id"], ondelete="CASCADE"),
        sa.ForeignKeyConstraint(
            ["source_id"], ["virtual_rom_sources.id"], ondelete="SET NULL"
        ),
        sa.PrimaryKeyConstraint("id"),
        sa.UniqueConstraint("rom_id", "url", name="uq_virtual_rom_mirror"),
        if_not_exists=True,
    )
    op.create_index(
        "ix_virtual_rom_mirrors_rom_id",
        "virtual_rom_mirrors",
        ["rom_id"],
        unique=False,
        if_not_exists=True,
    )


def downgrade() -> None:
    op.drop_table("virtual_rom_mirrors", if_exists=True)
